// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/aws/copilot-cli/internal/pkg/aws/identity"

	"github.com/aws/aws-sdk-go/aws/endpoints"

	awscfn "github.com/aws/copilot-cli/internal/pkg/aws/cloudformation"
	"github.com/aws/copilot-cli/internal/pkg/aws/s3"
	"github.com/aws/copilot-cli/internal/pkg/aws/sessions"
	"github.com/aws/copilot-cli/internal/pkg/config"
	"github.com/aws/copilot-cli/internal/pkg/deploy"
	"github.com/aws/copilot-cli/internal/pkg/deploy/cloudformation"
	"github.com/aws/copilot-cli/internal/pkg/deploy/cloudformation/stack"
	"github.com/aws/copilot-cli/internal/pkg/describe"
	"github.com/aws/copilot-cli/internal/pkg/manifest"
	"github.com/aws/copilot-cli/internal/pkg/template"
	"github.com/aws/copilot-cli/internal/pkg/term/color"
	"github.com/aws/copilot-cli/internal/pkg/term/log"
	termprogress "github.com/aws/copilot-cli/internal/pkg/term/progress"
	"github.com/aws/copilot-cli/internal/pkg/term/prompt"
	"github.com/aws/copilot-cli/internal/pkg/term/selector"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const (
	envUpgradeAppPrompt = "In which application is your environment?"

	envUpgradeEnvPrompt = "Which environment do you want to upgrade?"
	envUpgradeEnvHelp   = `Upgrades the AWS CloudFormation template for your environment
to support the latest Copilot features.`

	fmtEnvUpgradeStart    = "Upgrading environment %s from version %s to version %s."
	fmtEnvUpgradeFailed   = "Failed to upgrade environment %s's template to version %s.\n"
	fmtEnvUpgradeComplete = "Upgraded environment %s's template to version %s.\n"
)

// envUpgradeVars holds flag values.
type envUpgradeVars struct {
	appName string // Required. Name of the application.
	name    string // Required. Name of the environment.
	all     bool   // True means all environments should be upgraded.
}

// envUpgradeOpts represents the env upgrade command and holds the necessary data
// and clients to execute the command.
type envUpgradeOpts struct {
	envUpgradeVars

	store              store
	sel                appEnvSelector
	legacyEnvTemplater templater
	prog               progress
	appCFN             appResourcesGetter
	uploader           customResourcesUploader

	// Constructors for clients that can be initialized only at runtime.
	// These functions are overridden in tests to provide mocks.
	newEnvDeployer      func(conf *config.Environment) (environmentDeployer, error)
	newEnvVersionGetter func(app, env string) (versionGetter, error)
	newTemplateUpgrader func(conf *config.Environment) (envTemplateUpgrader, error)
	newS3               func(conf *config.Environment) (uploader, error)
}

func newEnvUpgradeOpts(vars envUpgradeVars) (*envUpgradeOpts, error) {
	sessProvider := sessions.ImmutableProvider(sessions.UserAgentExtras("env upgrade"))
	defaultSession, err := sessProvider.Default()
	if err != nil {
		return nil, err
	}
	store := config.NewSSMStore(identity.New(defaultSession), ssm.New(defaultSession), aws.StringValue(defaultSession.Config.Region))
	return &envUpgradeOpts{
		envUpgradeVars: vars,

		store: store,
		sel:   selector.NewAppEnvSelector(prompt.New(), store),
		legacyEnvTemplater: stack.NewEnvStackConfig(&deploy.CreateEnvironmentInput{
			Version: deploy.LegacyEnvTemplateVersion,
			App: deploy.AppInformation{
				Name: vars.appName,
			},
		}),
		prog:     termprogress.NewSpinner(log.DiagnosticWriter),
		uploader: template.New(),
		appCFN:   cloudformation.New(defaultSession),

		newEnvDeployer: func(conf *config.Environment) (environmentDeployer, error) {
			sess, err := sessProvider.FromRole(conf.ManagerRoleARN, conf.Region)
			if err != nil {
				return nil, fmt.Errorf("create environment stack deployer session from role %s and region %s: %v", conf.ManagerRoleARN, conf.Region, err)
			}
			return cloudformation.New(sess), nil
		},
		newEnvVersionGetter: func(app, env string) (versionGetter, error) {
			d, err := describe.NewEnvDescriber(describe.NewEnvDescriberConfig{
				App:         app,
				Env:         env,
				ConfigStore: store,
			})
			if err != nil {
				return nil, fmt.Errorf("new env describer for environment %s in app %s: %v", env, app, err)
			}
			return d, nil
		},
		newTemplateUpgrader: func(conf *config.Environment) (envTemplateUpgrader, error) {
			sess, err := sessProvider.FromRole(conf.ManagerRoleARN, conf.Region)
			if err != nil {
				return nil, fmt.Errorf("create template session from role %s and region %s: %v", conf.ManagerRoleARN, conf.Region, err)
			}
			return cloudformation.New(sess), nil
		},
		newS3: func(conf *config.Environment) (uploader, error) {
			sess, err := sessProvider.FromRole(conf.ManagerRoleARN, conf.Region)
			if err != nil {
				return nil, fmt.Errorf("create s3 session from role %s and region %s: %v", conf.ManagerRoleARN, conf.Region, err)
			}
			return s3.New(sess), nil
		},
	}, nil
}

// Validate returns an error if the values passed by flags are invalid.
func (o *envUpgradeOpts) Validate() error {
	if o.all && o.name != "" {
		return fmt.Errorf("cannot specify both --%s and --%s flags", allFlag, nameFlag)
	}
	if o.all {
		return nil
	}
	if o.name != "" {
		if _, err := o.store.GetEnvironment(o.appName, o.name); err != nil {
			var errEnvDoesNotExist *config.ErrNoSuchEnvironment
			if errors.As(err, &errEnvDoesNotExist) {
				return err
			}
			return fmt.Errorf("get environment %s configuration from application %s: %v", o.name, o.appName, err)
		}
	}
	return nil
}

// Ask prompts for any required flags that are not set by the user.
func (o *envUpgradeOpts) Ask() error {
	if o.appName == "" {
		app, err := o.sel.Application(envUpgradeAppPrompt, "")
		if err != nil {
			return fmt.Errorf("select application: %v", err)
		}
		o.appName = app
	}
	if !o.all && o.name == "" {
		env, err := o.sel.Environment(envUpgradeEnvPrompt, envUpgradeEnvHelp, o.appName)
		if err != nil {
			return fmt.Errorf("select environment: %v", err)
		}
		o.name = env
	}
	return nil
}

// Execute updates the cloudformation stack of an environment to the latest version.
// If the environment stack is busy updating, it spins and waits until the stack can be updated.
func (o *envUpgradeOpts) Execute() error {
	envs, err := o.listEnvsToUpgrade()
	if err != nil {
		return err
	}
	app, err := o.store.GetApplication(o.appName)
	if err != nil {
		return fmt.Errorf("get application %s: %w", o.appName, err)
	}
	for _, env := range envs {
		resources, err := o.appCFN.GetAppResourcesByRegion(app, env.Region)
		if err != nil {
			return fmt.Errorf("get app resources: %w", err)
		}
		if err := o.ensureManagerRoleIsAllowedToUpload(env, resources.S3Bucket); err != nil {
			return err
		}
		s3Client, err := o.newS3(env)
		if err != nil {
			return err
		}
		urls, err := o.uploader.UploadEnvironmentCustomResources(func(key string, objects ...s3.NamedBinary) (string, error) {
			return s3Client.ZipAndUpload(resources.S3Bucket, key, objects...)
		})
		if err != nil {
			return fmt.Errorf("upload custom resources to bucket %s: %w", resources.S3Bucket, err)
		}
		if err := o.upgrade(env, app, s3.FormatARN(endpoints.AwsPartitionID, resources.S3Bucket), resources.KMSKeyARN, urls); err != nil {
			return err
		}
	}
	return nil
}

// RecommendActions is a no-op for this command.
func (o *envUpgradeOpts) RecommendActions() error {
	return nil
}

func (o *envUpgradeOpts) listEnvsToUpgrade() ([]*config.Environment, error) {
	if !o.all {
		env, err := o.store.GetEnvironment(o.appName, o.name)
		if err != nil {
			return nil, fmt.Errorf("get environment %s in app %s: %w", o.appName, o.name, err)
		}
		return []*config.Environment{env}, nil
	}

	envs, err := o.store.ListEnvironments(o.appName)
	if err != nil {
		return nil, fmt.Errorf("list environments in app %s: %w", o.appName, err)
	}
	return envs, nil
}

func (o *envUpgradeOpts) ensureManagerRoleIsAllowedToUpload(env *config.Environment, bucketName string) error {
	cfn, err := o.newEnvDeployer(env)
	if err != nil {
		return err
	}
	body, err := cfn.EnvironmentTemplate(env.App, env.Name)
	if err != nil {
		return err
	}
	ok, err := o.isManagerRoleAllowedToUpload(body)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return o.grantManagerRolePermissionToUpload(cfn, env.App, env.Name, env.ExecutionRoleARN, body, s3.FormatARN(endpoints.AwsPartitionID, bucketName))
}

func (o *envUpgradeOpts) isManagerRoleAllowedToUpload(body string) (bool, error) {
	type Template struct {
		Metadata struct {
			Version string `yaml:"Version"`
		} `yaml:"Metadata"`
	}
	var tpl Template
	if err := yaml.Unmarshal([]byte(body), &tpl); err != nil {
		return false, fmt.Errorf("unmarshal environment template to detect Metadata.Version: %v", err)
	}
	if !semver.IsValid(tpl.Metadata.Version) { // The template doesn't contain a version.
		return false, nil
	}
	if semver.Compare(tpl.Metadata.Version, "v1.9.0") < 0 {
		// The permissions to grant the EnvManagerRole to upload artifacts was granted with template v1.9.0.
		return false, nil
	}
	return true, nil
}

func (o *envUpgradeOpts) grantManagerRolePermissionToUpload(cfn environmentDeployer, app, env, execRole, body, bucketARN string) error {
	// Detect which line number the EnvironmentManagerRole's PolicyDocument Statement is at.
	// We will add additional permissions after that line.
	type Template struct {
		Resources struct {
			ManagerRole struct {
				Properties struct {
					Policies []struct {
						Document struct {
							Statements yaml.Node `yaml:"Statement"`
						} `yaml:"PolicyDocument"`
					} `yaml:"Policies"`
				} `yaml:"Properties"`
			} `yaml:"EnvironmentManagerRole"`
		} `yaml:"Resources"`
	}

	var tpl Template
	if err := yaml.Unmarshal([]byte(body), &tpl); err != nil {
		return fmt.Errorf("unmarshal environment template to find EnvironmentManagerRole policy statement: %v", err)
	}
	if len(tpl.Resources.ManagerRole.Properties.Policies) == 0 {
		return errors.New("unable to find policies for the EnvironmentManagerRole")
	}
	// lines and columns are 1-indexed, so we have to substract one from each.
	statementLineIndex := tpl.Resources.ManagerRole.Properties.Policies[0].Document.Statements.Line - 1
	numSpaces := tpl.Resources.ManagerRole.Properties.Policies[0].Document.Statements.Column - 1
	pad := strings.Repeat(" ", numSpaces)

	// Create the additional permissions needed with the appropriate indentation.
	permissions := fmt.Sprintf(`- Sid: PatchPutObjectsToArtifactBucket
  Effect: Allow
  Action:
    - s3:PutObject
    - s3:PutObjectAcl
  Resource:
    - %s
    - %s/*`, bucketARN, bucketARN)
	permissions = pad + strings.Replace(permissions, "\n", "\n"+pad, -1)

	// Add the new permissions to the body.
	lines := strings.Split(body, "\n")
	linesBefore := lines[:statementLineIndex]
	linesAfter := lines[statementLineIndex:]
	updatedLines := append(linesBefore, append(strings.Split(permissions, "\n"), linesAfter...)...)
	updatedBody := strings.Join(updatedLines, "\n")

	// Update the Environment template with the new content.
	// CloudFormation is the only entity that's allowed to update the EnvManagerRole so we have to go through this route.
	// See #3556.
	var errEmptyChangeSet *awscfn.ErrChangeSetEmpty
	o.prog.Start("Update the environment's manager role with permission to upload artifacts to S3")
	err := cfn.UpdateEnvironmentTemplate(app, env, updatedBody, execRole)
	if err != nil && !errors.As(err, &errEmptyChangeSet) {
		o.prog.Stop(log.Serrorln("Unable to update the environment's manager role with upload artifacts permission"))
		return fmt.Errorf("update environment template with PutObject permissions: %v", err)
	}
	o.prog.Stop(log.Ssuccessln("Updated the environment's manager role with permissions to upload artifacts to S3"))
	return nil
}

func (o *envUpgradeOpts) upgrade(env *config.Environment, app *config.Application,
	artifactBucketARN, artifactBucketKeyARN string, customResourcesURLs map[string]string) (err error) {
	version, err := o.envVersion(env.Name)
	if err != nil {
		return err
	}
	if !shouldUpgradeEnv(env.Name, version) {
		return nil
	}

	o.prog.Start(fmt.Sprintf(fmtEnvUpgradeStart, color.HighlightUserInput(env.Name), color.Emphasize(version), color.Emphasize(deploy.LatestEnvTemplateVersion)))
	defer func() {
		if err != nil {
			o.prog.Stop(log.Serrorf(fmtEnvUpgradeFailed, color.HighlightUserInput(env.Name), color.Emphasize(deploy.LatestEnvTemplateVersion)))
			return
		}
		o.prog.Stop(log.Ssuccessf(fmtEnvUpgradeComplete, color.HighlightUserInput(env.Name), color.Emphasize(deploy.LatestEnvTemplateVersion)))
	}()
	upgrader, err := o.newTemplateUpgrader(env)
	if err != nil {
		return err
	}
	if version == deploy.LegacyEnvTemplateVersion {
		return o.upgradeLegacyEnvironment(upgrader, env, app, artifactBucketARN, artifactBucketKeyARN, customResourcesURLs, version, deploy.LatestEnvTemplateVersion)
	}
	return o.upgradeEnvironment(upgrader, env, app, artifactBucketARN, artifactBucketKeyARN, customResourcesURLs, version, deploy.LatestEnvTemplateVersion)
}

func (o *envUpgradeOpts) envVersion(name string) (string, error) {
	envTpl, err := o.newEnvVersionGetter(o.appName, name)
	if err != nil {
		return "", err
	}
	version, err := envTpl.Version()
	if err != nil {
		return "", fmt.Errorf("get template version of environment %s in app %s: %v", name, o.appName, err)
	}
	return version, err
}

func shouldUpgradeEnv(env, version string) bool {
	diff := semver.Compare(version, deploy.LatestEnvTemplateVersion)
	if diff < 0 {
		// Newer version available.
		return true
	}

	msg := fmt.Sprintf("Environment %s is already on the latest version %s, skip upgrade.", env, deploy.LatestEnvTemplateVersion)
	if diff > 0 {
		// It's possible that a teammate used a different version of the CLI to upgrade the environment
		// to a newer version. And the current user is on an older version of the CLI.
		// In this situation we notify them they should update the CLI.
		msg = fmt.Sprintf(`Skip upgrading environment %s to version %s since it's on version %s. 
Are you using the latest version of AWS Copilot?`, env, deploy.LatestEnvTemplateVersion, version)
	}
	log.Debugln(msg)
	return false
}

func (o *envUpgradeOpts) upgradeEnvironment(upgrader envUpgrader, conf *config.Environment, app *config.Application,
	artifactBucketARN, artifactBucketKeyARN string,
	customResourcesURLs map[string]string, fromVersion, toVersion string) error {
	var importedVPC *config.ImportVPC
	var adjustedVPC *config.AdjustVPC
	var importCertARNs []string
	var allowVPCIngressForInternalALB bool
	if conf.CustomConfig != nil {
		importedVPC = conf.CustomConfig.ImportVPC
		adjustedVPC = conf.CustomConfig.VPCConfig
		importCertARNs = conf.CustomConfig.ImportCertARNs
		allowVPCIngressForInternalALB = conf.CustomConfig.EnableInternalALBVPCIngress
	}

	if err := upgrader.UpgradeEnvironment(&deploy.CreateEnvironmentInput{
		Version: toVersion,
		App: deploy.AppInformation{
			Name:   app.Name,
			Domain: app.Domain,
		},
		Name:                 conf.Name,
		ArtifactBucketKeyARN: artifactBucketKeyARN,
		ArtifactBucketARN:    artifactBucketARN,
		CustomResourcesURLs:  customResourcesURLs,
		ImportVPCConfig:      importedVPC,
		AdjustVPCConfig:      adjustedVPC,
		ImportCertARNs:       importCertARNs,
		CFNServiceRoleARN:    conf.ExecutionRoleARN,
		Telemetry:            conf.Telemetry,
		AllowVPCIngress:      allowVPCIngressForInternalALB,
	}); err != nil {
		return fmt.Errorf("upgrade environment %s from version %s to version %s: %v", conf.Name, fromVersion, toVersion, err)
	}
	return nil
}

func (o *envUpgradeOpts) upgradeLegacyEnvironment(upgrader legacyEnvUpgrader, conf *config.Environment, app *config.Application,
	artifactBucketARN, artifactBucketKeyARN string,
	customResourcesURLs map[string]string, fromVersion, toVersion string) error {
	isDefaultEnv, err := o.isDefaultLegacyTemplate(upgrader, conf.App, conf.Name)
	if err != nil {
		return err
	}
	albWorkloads, err := o.listLBWebServices()
	if err != nil {
		return err
	}
	if isDefaultEnv {
		if err := upgrader.UpgradeLegacyEnvironment(&deploy.CreateEnvironmentInput{
			Version: toVersion,
			App: deploy.AppInformation{
				Name:   app.Name,
				Domain: app.Domain,
			},
			Name:                 conf.Name,
			ArtifactBucketKeyARN: artifactBucketKeyARN,
			ArtifactBucketARN:    artifactBucketARN,
			CustomResourcesURLs:  customResourcesURLs,
			CFNServiceRoleARN:    conf.ExecutionRoleARN,
			Telemetry:            conf.Telemetry,
		}, albWorkloads...); err != nil {
			return fmt.Errorf("upgrade environment %s from version %s to version %s: %v", conf.Name, fromVersion, toVersion, err)
		}
		return nil
	}
	return o.upgradeLegacyEnvironmentWithVPCOverrides(upgrader, conf, fromVersion, toVersion, albWorkloads)
}

func (o *envUpgradeOpts) isDefaultLegacyTemplate(cfn envTemplater, appName, envName string) (bool, error) {
	defaultLegacyEnvTemplate, err := o.legacyEnvTemplater.Template()
	if err != nil {
		return false, fmt.Errorf("generate default legacy environment template: %v", err)
	}
	actualTemplate, err := cfn.EnvironmentTemplate(appName, envName)
	if err != nil {
		return false, fmt.Errorf("get environment %s template body: %v", envName, err)
	}
	return defaultLegacyEnvTemplate == actualTemplate, nil
}

func (o *envUpgradeOpts) listLBWebServices() ([]string, error) {
	services, err := o.store.ListServices(o.appName)
	if err != nil {
		return nil, fmt.Errorf("list services in application %s: %v", o.appName, err)
	}
	var lbWebServiceNames []string
	for _, svc := range services {
		if svc.Type != manifest.LoadBalancedWebServiceType {
			continue
		}
		lbWebServiceNames = append(lbWebServiceNames, svc.Name)
	}
	return lbWebServiceNames, nil
}

func (o *envUpgradeOpts) upgradeLegacyEnvironmentWithVPCOverrides(upgrader legacyEnvUpgrader, conf *config.Environment,
	fromVersion, toVersion string, albWorkloads []string) error {
	if conf.CustomConfig != nil {
		if err := upgrader.UpgradeLegacyEnvironment(&deploy.CreateEnvironmentInput{
			Version: toVersion,
			App: deploy.AppInformation{
				Name: conf.App,
			},
			Name:              conf.Name,
			ImportVPCConfig:   conf.CustomConfig.ImportVPC,
			AdjustVPCConfig:   conf.CustomConfig.VPCConfig,
			ImportCertARNs:    conf.CustomConfig.ImportCertARNs,
			AllowVPCIngress:   conf.CustomConfig.EnableInternalALBVPCIngress,
			CFNServiceRoleARN: conf.ExecutionRoleARN,
		}, albWorkloads...); err != nil {
			return fmt.Errorf("upgrade environment %s from version %s to version %s: %v", conf.Name, fromVersion, toVersion, err)
		}
		return nil
	}
	// Prior to #1433, we did not store the custom VPC config in SSM.
	// In this situation we unfortunately have to ask the users to enter the VPC configuration into SSM or re-create the
	// environment in case they run into this issue.
	log.Warningln(`
Looks like you've an environment with a customized VPC configuration.
Copilot could not upgrade your environment's CloudFormation template.
To learn more about how to fix it: https://github.com/aws/copilot-cli/issues/1601`)
	return errors.New("cannot upgrade environment due to missing vpc configuration")
}

// buildEnvUpgradeCmd builds the command to update environment(s) to the latest version of
// the environment template.
func buildEnvUpgradeCmd() *cobra.Command {
	vars := envUpgradeVars{}
	cmd := &cobra.Command{
		Use:    "upgrade",
		Short:  "Upgrades the template of an environment to the latest version.",
		Hidden: true,
		RunE: runCmdE(func(cmd *cobra.Command, args []string) error {
			opts, err := newEnvUpgradeOpts(vars)
			if err != nil {
				return err
			}
			return run(opts)
		}),
	}
	cmd.Flags().StringVarP(&vars.name, nameFlag, nameFlagShort, "", envFlagDescription)
	cmd.Flags().StringVarP(&vars.appName, appFlag, appFlagShort, tryReadingAppName(), appFlagDescription)
	cmd.Flags().BoolVar(&vars.all, allFlag, false, upgradeAllEnvsDescription)
	return cmd
}
