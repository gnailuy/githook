#!/bin/bash

WORKDIR=/home/yuliang/
GITROOT=$WORKDIR/gnailuy.com/
WBEROOT=$WORKDIR/webroot/

GIT_BIN=/usr/bin/git
JEKYLL_BIN=/usr/local/bin/jekyll

# Update Git Repo
cd $GITROOT
$GIT_BIN pull
$GIT_BIN submodule update

# Build Site
cd $GITROOT
$JEKYLL_BIN build

# Deploy Site
rm -r $WBEROOT/*
mv $GITROOT/_site/* $WBEROOT

