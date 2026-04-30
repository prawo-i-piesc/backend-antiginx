# Variables
ARG BACKEND_BINARY_NAME=backend
ARG TARGETARCH

ARG USERNAME=antiginx_user
ARG GROUPNAME=antiginx_group
ARG USER_UID=1001
ARG USER_GID=1001
# ---


# Base images for stages
FROM golang:1.26-alpine AS base
FROM alpine:3.23 AS run
# ---


# STAGE: Install dependencies
FROM base AS deps

WORKDIR /app

COPY go.mod ./
COPY go.sum ./

RUN if [ -f go.mod ] && [ -f go.sum ]; then          \
        go mod download;                             \
    else                                             \
        echo "No go.mod or go.sum found" && exit 1;  \
    fi
# ---


# STAGE: Build the application
FROM base AS build

ARG BACKEND_BINARY_NAME
ARG TARGETARCH

WORKDIR /app

COPY go.mod ./
COPY go.sum ./

COPY --from=deps /go/pkg/mod /go/pkg/mod

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -o ./${BACKEND_BINARY_NAME} .
# ---


# STAGE: Final image to run the application
FROM run AS runner

ARG BACKEND_BINARY_NAME
ENV BACKEND_BINARY_NAME=${BACKEND_BINARY_NAME}

ARG USERNAME
ARG GROUPNAME
ARG USER_UID
ARG USER_GID

WORKDIR /app

RUN apk --no-cache upgrade &&          \
    apk --no-cache add ca-certificates

RUN addgroup -g ${USER_GID} -S ${GROUPNAME}
RUN adduser -u ${USER_UID} -S ${USERNAME} -G ${GROUPNAME}

COPY --from=build --chown=${USERNAME}:${GROUPNAME} /app/${BACKEND_BINARY_NAME} ./${BACKEND_BINARY_NAME}

USER ${USERNAME}

EXPOSE 4000

CMD ["sh", "-c", "./$BACKEND_BINARY_NAME"]