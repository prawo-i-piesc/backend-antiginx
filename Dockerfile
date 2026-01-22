# Build stage: compile the Go application
FROM golang:1.25-alpine AS build

WORKDIR /app

# Copy the Go module files
COPY go.mod ./
COPY go.sum ./

# Download the Go module dependencies
RUN go mod download

COPY . .

# Build - TARGETARCH is automatically set by Docker Buildx for multi-arch builds
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -o /backend-antiginx .

# Final stage: a minimal image to run the application
FROM alpine:3.21 AS run

# Install ca-certificates
RUN apk --no-cache upgrade && \
    apk --no-cache add ca-certificates

# Create non-root user for security
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

WORKDIR /app

# Copy the application executable from the build image
COPY --from=build /backend-antiginx /backend-antiginx

# Set ownership and switch to non-root user
RUN chown -R appuser:appgroup /backend-antiginx
USER appuser

# Document the port used by the application
EXPOSE 4000
CMD ["/backend-antiginx"]