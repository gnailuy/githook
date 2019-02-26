githook
=======

Github Hook Service for [gnailuy.com](http://gnailuy.com/).

Please read [this blog post](http://gnailuy.com/jekyll/2014/08/29/github-webhook/) (in Chinese).

### Prepare the site

``` bash
WORKDIR=/home/yuliang/
mkdir -p $WORKDIR/webroot

git clone https://github.com/gnailuy/gnailuy.com.git
cd gnailuy.com
git submodule init
git submodule update

# Copy environment.rc to /etc/ and edit it
```

### Build the image

``` bash
# Build gnailuy/jekyll first. See http://github.com/gnailuy/gnailuy.com/
docker build -t gnailuy/githook .
```

### Run on Docker

``` bash
WORKDIR=/home/yuliang/
docker run -d --name githook --env-file /etc/environment.rc -v ${WORKDIR}/gnailuy.com/:/app/gnailuy.com/ -v ${WORKDIR}/webroot:/app/webroot/ -p 20182:20182 -t gnailuy/githook
```

### Run Githook as a local service

``` bash
./githook-service start
```

