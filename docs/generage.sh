#!/bin/bash

which swag > /dev/null
if [ $? -ne 0 ]; then
	echo "Swag CLI not found. Please install it by running 'go install github.com/swag/cmd/swag@latest'"
	exit 1
fi

# Generate Swagger docs
swag init -g cmd/main.go -o docs