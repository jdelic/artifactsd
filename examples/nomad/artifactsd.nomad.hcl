# Example Nomad job for artifactsd.
#
# Prerequisites:
#   1. Configure a host volume on every client that may run this job.
#      In the Nomad agent config (nomad.hcl or /etc/nomad.d/client.hcl):
#
#        client {
#          host_volume "artifactsd-data" {
#            path      = "/opt/artifactsd-data"
#            read_only = false
#          }
#        }
#
#      Create the directory and restart the client agent:
#        sudo mkdir -p /opt/artifactsd-data
#        sudo chown nobody:nogroup /opt/artifactsd-data   # distroless nonroot uid
#        sudo systemctl restart nomad
#
#   2. Ensure the Docker image is reachable from your clients:
#        docker pull registry.maurus.net/agent-tools/artifactsd:latest
#
#   3. Replace every "example.com" placeholder with real values.
#
# ─── IMPORTANT: count must remain 1 ────────────────────────────────────────
#   artifactsd uses in-process SQLite. Running more than one allocation would
#   cause concurrent writers to contend on the same database file — the second
#   writer will error-loop. Horizontal scaling is explicitly not a design goal.
#   For mass distribution, put a CDN or object-storage proxy in front.
# ────────────────────────────────────────────────────────────────────────────

job "artifactsd" {
  datacenters = ["dc1"]
  type        = "service"

  group "artifactsd" {
    # DO NOT raise count above 1 — see note above.
    count = 1

    volume "data" {
      type      = "host"
      source    = "artifactsd-data"
      read_only = false
    }

    network {
      port "http" {
        to           = 8080
        # static = 8080  # uncomment to pin the host port
      }
    }

    restart {
      attempts = 5
      delay    = "10s"
      interval = "2m"
      mode     = "delay"
    }

    task "artifactsd" {
      driver = "docker"

      config {
        image = "registry.maurus.net/agent-tools/artifactsd:latest"
        ports = ["http"]
      }

      volume_mount {
        volume      = "data"
        destination = "/data"
        read_only   = false
      }

      env {
        # Required: the public URL clients use to reach this server.
        ARTIFACTSD_BASE_URL = "https://artifacts.example.com"
        ARTIFACTSD_LISTEN   = ":8080"
        ARTIFACTSD_DATA_DIR = "/data"

        # TLS: set to "acme" and configure ARTIFACTSD_ACME_DOMAINS /
        # ARTIFACTSD_ACME_EMAIL if this allocation terminates TLS directly.
        # Leave "off" when a Nomad ingress gateway or external proxy handles it.
        ARTIFACTSD_TLS = "off"

        # Bootstrap token: seeds the first admin-scoped API token on first
        # boot. Replace "change-me" with a random value (e.g. `openssl rand
        # -hex 32`) and remove this line once you have created a real token.
        ARTIFACTSD_BOOTSTRAP_TOKEN = "change-me"
      }

      resources {
        cpu    = 256  # MHz — increase if serving many concurrent downloads
        memory = 256  # MiB
      }

      service {
        name     = "artifactsd"
        port     = "http"
        provider = "consul"  # change to "nomad" if not using Consul

        tags = [
          "traefik.enable=true",
          "traefik.http.routers.artifactsd.rule=Host(`artifacts.example.com`)",
          "traefik.http.routers.artifactsd.tls=true",
          "traefik.http.routers.artifactsd.tls.certresolver=le",
        ]

        check {
          type     = "http"
          path     = "/healthz"
          interval = "30s"
          timeout  = "5s"
        }
      }
    }
  }
}
