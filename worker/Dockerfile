# See: http://github.com/gnailuy/gnailuy.com for the image `gnailuy/jekyll`
FROM gnailuy/jekyll

WORKDIR /app

COPY . .
RUN mkdir -p logs && apk add --no-cache git openssh && gem install sinatra redis

ENTRYPOINT ["ruby", "worker.rb"]

