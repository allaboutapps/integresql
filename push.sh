#!/bin/sh

IMAGE_NAME=integresql

docker build -t ${IMAGE_NAME} .
docker tag ${IMAGE_NAME} allaboutapps/${IMAGE_NAME}
docker push allaboutapps/integresql:latest

