# Build stage: compile the Go application
FROM golang:latest AS build

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
FROM alpine:latest AS run

# Install ca-certificates for HTTPS connections
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the application executable from the build image
COPY --from=build /backend-antiginx /backend-antiginx

# Document the port used by the application
EXPOSE 4000
CMD ["/backend-antiginx"]