# --- Builder Stage ---
# Use a Go 1.24 base image
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies (git for go modules)
RUN apk add --no-cache git

# Copy go module files and download dependencies first for layer caching
# Ensure go.mod reflects 'go 1.24' before building
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the Go application statically linked (optional but good for alpine)
# CGO_ENABLED=0 prevents potential glibc dependencies if using certain packages
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /web-k8s-terminal main.go

# --- Runtime Stage ---
FROM alpine:3.21

WORKDIR /app

# Install runtime dependencies:
# - bash (better shell experience than sh)
# - ca-certificates (for HTTPS calls, kubectl needs this)
# - curl (to download kubectl)
# - kubectl (the k8s client binary)
RUN apk add --no-cache bash ca-certificates curl \
    && KUBE_LATEST_VERSION=$(curl -s https://storage.googleapis.com/kubernetes-release/release/stable.txt) \
    && echo "Downloading kubectl version: ${KUBE_LATEST_VERSION}" \
    && curl -LO "https://storage.googleapis.com/kubernetes-release/release/${KUBE_LATEST_VERSION}/bin/linux/amd64/kubectl" \
    && chmod +x ./kubectl \
    && mv ./kubectl /usr/local/bin/kubectl \
    && kubectl version --client

# Copy the built application binary from the builder stage
COPY --from=builder /web-k8s-terminal /usr/local/bin/web-k8s-terminal

# Copy the static frontend files
COPY static ./static

# Define the GID/UID we want to use (still useful for the adduser/addgroup commands)
ARG APP_GID=10001
ARG APP_UID=10001

# Create a non-root group and user with specific UID/GID in the desired range
# Use the ARGs here for consistency in user creation
RUN addgroup -g ${APP_GID} appgroup && \
    adduser -D -u ${APP_UID} -G appgroup appuser

# Switch to the non-root user using the EXPLICIT numeric UID
# This is the most direct way to satisfy the CKV_CHOREO_1 check
USER 10001

# Expose the port the application listens on
EXPOSE 8080

# Run the application
# Use bash as the default shell if available when the app starts it via PTY
ENV SHELL=/bin/bash
ENTRYPOINT ["web-k8s-terminal"]