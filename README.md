githook
=======

Github Hook Service for [gnailuy.com](http://gnailuy.com/).

### Prepare the site directory

``` bash
WORKDIR=/home/yourusername/
mkdir -p $WORKDIR/webroot
mkdir -p $WORKDIR/redis_data
mkdir -p $WORKDIR/logs

git clone https://github.com/gnailuy/gnailuy.com.git
cd gnailuy.com
git submodule init
git submodule update

# Copy `environment.rc` to current directory and edit it according to your configuration
```

### Build the image

``` bash
# Build `gnailuy/jekyll` first. See: http://github.com/gnailuy/gnailuy.com/

# In `server`
docker build -t gnailuy/githook_server .

# In `worker`
docker build -t gnailuy/githook_worker .
```

### Run on Docker

``` bash
WORKDIR=/home/yourusername/

# Create network (run once)
docker network create githook

# Run redis
docker run -d --restart unless-stopped --network githook --name redis -v ${WORKDIR}/githook/redis/redis.conf:/etc/redis/redis.conf -v ${WORKDIR}/redis_data:/data -t redis redis-server /etc/redis/redis.conf

# Run the worker
docker run -d --restart unless-stopped --network githook --name githook_worker -v ${WORKDIR}/gnailuy.com/:/app/gnailuy.com/ -v ${WORKDIR}/webroot:/app/webroot/ -v ${WORKDIR}/logs:/app/logs/ -t gnailuy/githook_worker

# Run the server
docker run -d --restart unless-stopped --network githook --name githook_server --env-file ./environment.rc -v ${WORKDIR}/logs:/app/logs/ -p 20182:20182 -t gnailuy/githook_server
```

### Connect to redis

``` bash
docker run -it --network githook --rm redis redis-cli -h redis
```

