FROM node:24-slim AS chromium

WORKDIR /tmp/chromium
RUN npm pack @sparticuz/chromium@143.0.4 --silent \
  && tar -xzf sparticuz-chromium-143.0.4.tgz package/bin \
  && mkdir -p /opt/chromium \
  && cp package/bin/*.br /opt/chromium/

FROM golang:1.25 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/bootstrap ./cmd/render-worker

FROM public.ecr.aws/lambda/provided:al2023

COPY --from=build /out/bootstrap /var/task/bootstrap
COPY --from=chromium /opt/chromium /opt/chromium

ENV CHROMIUM_BIN_DIR=/opt/chromium

CMD ["bootstrap"]

