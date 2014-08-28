#!/bin/bash

WORKDIR=/home/yuliang/
GITROOT=$WORKDIR/gnailuy.com/
WBEROOT=$WORKDIR/webroot/

# Update Git Repo
cd $GITROOT
git pull
git submodule update

# Build Site
cd $GITROOT
jekyll build

# Deploy Site
rm -r $WBEROOT/*
mv $GITROOT/_site/* $WBEROOT
