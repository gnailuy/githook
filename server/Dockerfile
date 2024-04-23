# See: http://github.com/gnailuy/gnailuy.com for the image `gnailuy/jekyll`
FROM gnailuy/jekyll

WORKDIR /app

COPY . .
RUN mkdir -p logs && gem install sinatra redis rackup

EXPOSE 20182
ENTRYPOINT ["ruby", "server.rb"]

