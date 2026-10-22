http:
  routers:
    control:
      entryPoints: [web]
      rule: >-
        PathPrefix(`/platform/instances`) ||
        PathPrefix(`/platform/topology`) ||
        PathPrefix(`/platform/events`) ||
        PathPrefix(`/platform/command-log`)
      priority: 110
      service: control
    platform-api:
      entryPoints: [web]
      rule: PathPrefix(`/platform`) || Path(`/.well-known/jwks.json`)
      priority: 100
      service: platform-api
    gateway:
      entryPoints: [web]
      rule: PathPrefix(`/ws`)
      priority: 100
      service: gateway
    admin:
      entryPoints: [web]
      rule: PathPrefix(`/admin`)
      priority: 50
      service: admin
    frontend:
      entryPoints: [web]
      rule: PathPrefix(`/`)
      priority: 1
      service: frontend

  services:
    control:
      loadBalancer:
        healthCheck:
          path: /healthz
          interval: 2s
          timeout: 1s
        servers:
          - url: http://control-plane:8080
    platform-api:
      loadBalancer:
        healthCheck:
          path: /platform/healthz
          interval: 2s
          timeout: 1s
        servers:
          - url: http://platform-api-1:8080
          - url: http://__SECONDARY_HOST__:__PLATFORM_API_B_PORT__
    gateway:
      loadBalancer:
        healthCheck:
          path: /healthz
          interval: 2s
          timeout: 1s
        servers:
          - url: http://gateway-1:8080
          - url: http://__SECONDARY_HOST__:__GATEWAY_B_PORT__
    admin:
      loadBalancer:
        servers:
          - url: http://admin-console:8080
    frontend:
      loadBalancer:
        servers:
          - url: http://frontend:80
