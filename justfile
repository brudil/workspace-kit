# Build everything, and the example plugin to examples/hello/ws-hello.
build:
    go build ./...
    cd examples/hello && go build -o ws-hello .

# The framework's tests, then the example's.
test:
    go test ./...
    cd examples/hello && go test ./...

check:
    gofmt -l . | grep . && exit 1 || true
    go vet ./...
    cd examples/hello && go vet ./...
    go build ./...

# Build the example's app into examples/hello/ui/dist, which is committed:
# Go alone builds the plugin. It needs Node and npm.
ui:
    cd examples/hello/ui && npm ci && npm run build

# Fail if the example's committed app isn't what its source builds.
ui-check: ui
    git diff --exit-code -- examples/hello/ui/dist
