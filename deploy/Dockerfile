# Stage 1: Build Vue frontend
FROM node:22-alpine AS frontend
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
COPY internal/coordinator/frontend/.gitkeep ../internal/coordinator/frontend/.gitkeep
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.24-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /internal/coordinator/frontend/ internal/coordinator/frontend/
RUN CGO_ENABLED=0 GOOS=linux go build -o /boss ./cmd/boss/

# Stage 3: Minimal runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /boss /boss
EXPOSE 8899
ENTRYPOINT ["/boss", "serve"]
