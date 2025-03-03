List of all available properties for a `'Load Balanced Web Service'` manifest. To learn about Copilot services, see the [Services](../concepts/services.en.md) concept page.

???+ note "Sample manifest for a frontend service"

    ```yaml
        # Your service name will be used in naming your resources like log groups, ECS services, etc.
        name: frontend
        type: Load Balanced Web Service

        # Distribute traffic to your service.
        http:
          path: '/'
          healthcheck:
            path: '/_healthcheck'
            port: 8080
            success_codes: '200,301'
            healthy_threshold: 3
            unhealthy_threshold: 2
            interval: 15s
            timeout: 10s
            grace_period: 45s
          deregistration_delay: 5s
          stickiness: false
          allowed_source_ips: ["10.24.34.0/23"]
          alias: example.com

        nlb:
          port: 443/tls

        # Configuration for your containers and service.
        image:
          build:
            dockerfile: ./frontend/Dockerfile
            context: ./frontend
          port: 80

        cpu: 256
        memory: 512
        count:
          range: 1-10
          cooldown:
            in: 30s
            out: 60s
          cpu_percentage: 70
          memory_percentage:
            value: 80
            cooldown:
              in: 80s
              out: 160s
          requests: 10000
          response_time: 2s
        exec: true

        variables:
          LOG_LEVEL: info
        env_file: log.env
        secrets:
          GITHUB_TOKEN: GITHUB_TOKEN

        # You can override any of the values defined above by environment.
        environments:
          test:
            count:
              range:
                min: 1
                max: 10
                spot_from: 2
          staging:
            count:
              spot: 2
          production:
            count: 2
    ```

<a id="name" href="#name" class="field">`name`</a> <span class="type">String</span>  
The name of your service.

<div class="separator"></div>

<a id="type" href="#type" class="field">`type`</a> <span class="type">String</span>  
The architecture type for your service. A [Load Balanced Web Service](../concepts/services.en.md#load-balanced-web-service) is an internet-facing service that's behind a load balancer, orchestrated by Amazon ECS on AWS Fargate.

<div class="separator"></div>

<a id="http" href="#http" class="field">`http`</a> <span class="type">Boolean or Map</span>  
The http section contains parameters related to integrating your service with an Application Load Balancer.

To disable the Application Load Balancer, specify `http: false`. Note that for a Load-Balanced Web Service,
at least one of Application Load Balancer or Network Load Balancer must be enabled.

<span class="parent-field">http.</span><a id="http-path" href="#http-path" class="field">`path`</a> <span class="type">String</span>  
Requests to this path will be forwarded to your service. Each Load Balanced Web Service should listen on a unique path.

{% include 'http-healthcheck.en.md' %}

<span class="parent-field">http.</span><a id="http-deregistration-delay" href="#http-deregistration-delay" class="field">`deregistration_delay`</a> <span class="type">Duration</span>  
The amount of time to wait for targets to drain connections during deregistration. The default is 60s. Setting this to a larger value gives targets more time to gracefully drain connections, but increases the time required for new deployments. Range 0s-3600s.

<span class="parent-field">http.</span><a id="http-target-container" href="#http-target-container" class="field">`target_container`</a> <span class="type">String</span>  
A sidecar container that takes the place of a service container.

<span class="parent-field">http.</span><a id="http-stickiness" href="#http-stickiness" class="field">`stickiness`</a> <span class="type">Boolean</span>  
Indicates whether sticky sessions are enabled.

<span class="parent-field">http.</span><a id="http-allowed-source-ips" href="#http-allowed-source-ips" class="field">`allowed_source_ips`</a> <span class="type">Array of Strings</span>  
CIDR IP addresses permitted to access your service.
```yaml
http:
allowed_source_ips: ["192.0.2.0/24", "198.51.100.10/32"]
```

<span class="parent-field">http.</span><a id="http-alias" href="#http-alias" class="field">`alias`</a> <span class="type">String or Array of Strings or Array of Maps</span>  
HTTPS domain alias of your service.
```yaml
# String version.
http:
  alias: example.com
# Alternatively, as an array of strings.
http:
  alias: ["example.com", "v1.example.com"]
# Alternatively, as an array of maps.
http:
  alias:
    - name: example.com
      hosted_zone: Z0873220N255IR3MTNR4
    - name: v1.example.com
      hosted_zone: AN0THE9H05TED20NEID
```
<span class="parent-field">http.</span><a id="http-hosted-zone" href="#http-hosted-zone" class="field">`hosted_zone`</a> <span class="type">String</span>  
ID of your existing hosted zone; must be used with `http.alias`. If you have an environment with imported certificates, you can specify the hosted zone into which Copilot should insert the A record once the load balancer is created.
```yaml
http:
  alias: example.com
  hosted_zone: Z0873220N255IR3MTNR4
# Also see http.alias array of maps example, above.
```
<span class="parent-field">http.</span><a id="http-version" href="#http-version" class="field">`version`</a> <span class="type">String</span>  
The HTTP(S) protocol version. Must be one of `'grpc'`, `'http1'`, or `'http2'`. If omitted, then `'http1'` is assumed.
If using gRPC, please note that a domain must be associated with your application.

{% include 'nlb.en.md' %}

{% include 'image-config-with-port.en.md' %}

{% include 'image-healthcheck.en.md' %}

{% include 'task-size.en.md' %}

{% include 'platform.en.md' %}

<div class="separator"></div>

<a id="count" href="#count" class="field">`count`</a> <span class="type">Integer or Map</span>  
If you specify a number:
```yaml
count: 5
```
The service will set the desired count to 5 and maintain 5 tasks in your service.

<span class="parent-field">count.</span><a id="count-spot" href="#count-spot" class="field">`spot`</a> <span class="type">Integer</span>

If you want to use Fargate Spot capacity to run your services, you can specify a number under the `spot` subfield:
```yaml
count:
  spot: 5
```
!!! info
    Fargate Spot is not supported for containers running on ARM architecture.

<div class="separator"></div>

Alternatively, you can specify a map for setting up autoscaling:
```yaml
count:
  range: 1-10
  cooldown:
    in: 30s
    out: 60s
  cpu_percentage: 70
  memory_percentage:
    value: 80
    cooldown:
      in: 80s
      out: 160s
  requests: 10000
  response_time: 2s
```

<span class="parent-field">count.</span><a id="count-range" href="#count-range" class="field">`range`</a> <span class="type">String or Map</span>  
You can specify a minimum and maximum bound for the number of tasks your service should maintain, based on the values you specify for the metrics.
```yaml
count:
  range: n-m
```
This will set up an Application Autoscaling Target with the `MinCapacity` of `n` and `MaxCapacity` of `m`.

Alternatively, if you wish to scale your service onto Fargate Spot instances, specify `min` and `max` under `range` and then specify `spot_from` with the desired count you wish to start placing your services onto Spot capacity. For example:

```yaml
count:
  range:
    min: 1
    max: 10
    spot_from: 3
```

This will set your range as 1-10 as above, but will place the first two copies of your service on dedicated Fargate capacity. If your service scales to 3 or higher, the third and any additional copies will be placed on Spot until the maximum is reached.

<span class="parent-field">range.</span><a id="count-range-min" href="#count-range-min" class="field">`min`</a> <span class="type">Integer</span>
The minimum desired count for your service using autoscaling.

<span class="parent-field">range.</span><a id="count-range-max" href="#count-range-max" class="field">`max`</a> <span class="type">Integer</span>
The maximum desired count for your service using autoscaling.

<span class="parent-field">range.</span><a id="count-range-spot-from" href="#count-range-spot-from" class="field">`spot_from`</a> <span class="type">Integer</span>
The desired count at which you wish to start placing your service using Fargate Spot capacity providers.

<span class="parent-field">count.</span><a id="count-cooldown" href="#count-cooldown" class="field">`cooldown`</a> <span class="type">Map</span>
Cooldown scaling fields that are used as the default cooldown for all autoscaling fields specified.

<span class="parent-field">count.cooldown.</span><a id="count-cooldown-in" href="#count-cooldown-in" class="field">`in`</a> <span class="type">Duration</span>
The cooldown time for autoscaling fields to scale up the service.

<span class="parent-field">count.cooldown.</span><a id="count-cooldown-out" href="#count-cooldown-out" class="field">`out`</a> <span class="type">Duration</span>
The cooldown time for autoscaling fields to scale down the service.

The following options `cpu_percentage`, `memory_percentage`, `requests` and `response_time` are autoscaling fields for `count` which can be defined either as the value of the field, or as a Map containing advanced information about the field's `value` and `cooldown`:
```yaml
value: 50
cooldown:
  in: 30s
  out: 60s
```
The cooldown specified here will override the default cooldown.

<span class="parent-field">count.</span><a id="count-cpu-percentage" href="#count-cpu-percentage" class="field">`cpu_percentage`</a> <span class="type">Integer or Map</span>
Scale up or down based on the average CPU your service should maintain.

<span class="parent-field">count.</span><a id="count-memory-percentage" href="#count-memory-percentage" class="field">`memory_percentage`</a> <span class="type">Integer or Map</span>
Scale up or down based on the average memory your service should maintain.

<span class="parent-field">count.</span><a id="requests" href="#count-requests" class="field">`requests`</a> <span class="type">Integer or Map</span>
Scale up or down based on the request count handled per task.

<span class="parent-field">count.</span><a id="response-time" href="#count-response-time" class="field">`response_time`</a> <span class="type">Duration or Map</span>
Scale up or down based on the service average response time.

{% include 'exec.en.md' %}

{% include 'deployment.en.md' %}

{% include 'entrypoint.en.md' %}

{% include 'command.en.md' %}

{% include 'network.en.md' %}

{% include 'envvars.en.md' %}

{% include 'secrets.en.md' %}

{% include 'storage.en.md' %}

{% include 'publish.en.md' %}

{% include 'logging.en.md' %}

{% include 'observability.en.md' %}

{% include 'taskdef-overrides.en.md' %}

{% include 'environments.en.md' %}
