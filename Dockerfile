# Build stage: compile the Go application
FROM golang:latest AS build

WORKDIR /app

# Copy the Go module files
COPY go.mod ./
COPY main.go ./

# Download the Go module dependencies
RUN go mod download

COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /backend-antigingx


# Final stage: a minimal image to run the application
FROM alpine:latest AS run

WORKDIR /app

# Copy the application executable from the build image
COPY --from=build /backend-antigingx ./

EXPOSE 8080
CMD ["./backend-antigingx"]