#!/bin/bash

#static 3MB build, no distro, no shell :)
docker build . -t urtho/algonode:latest
docker push urtho/algonode:latest
