### Prerequisites

```shell
docker rm -f buildkitd 2>/dev/null || true
docker run -d --name buildkitd \
  --privileged \
  -p 1234:1234 \
  moby/buildkit:v0.25.0 \
  --addr tcp://0.0.0.0:1234 \
  --oci-worker=true \
  --oci-worker-platform=linux/arm64
  ```