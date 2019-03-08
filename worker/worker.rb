require 'logger'
require 'redis'

$log = Logger.new('logs/githook_worker.log', 0, 100 * 1024 * 1024)
$log.level = Logger::INFO

QUEUENAME = 'githook'
redis = Redis.new(host: 'redis', port: 6379, db: 0)

while true
  task = redis.brpop(QUEUENAME)
  qlen = redis.llen(QUEUENAME)
  $log.info("Task Received: #{task}; remaining tasks in queue #{qlen}")
  system('sh update.sh >> logs/update.log 2>&1')
  $log.info("Task Finished: #{task}")
end

