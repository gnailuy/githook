require 'redis'

redis = Redis.new(host: 'redis', port: 6379, db: 0)

while true
  task = redis.brpop('githook')
  puts "Received Task: #{task}"
  system('sh update.sh >> logs/update.log 2>&1')
end

