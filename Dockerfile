FROM golang:1.24-alpine

WORKDIR /app

# Copy go files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build for current architecture
RUN go build -v -o ./main ./cmd/api

# Expose port
EXPOSE 8080

# Run
ENTRYPOINT ["./main"]
