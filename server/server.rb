require 'json'
require 'logger'
require 'redis'
require 'sinatra'

set :bind, '0.0.0.0'
set :port, 20182

$log = Logger.new('logs/githook_server.log', 0, 100 * 1024 * 1024)
$log.level = Logger::INFO

QUEUENAME = 'githook'
redis = Redis.new(host: 'redis', port: 6379, db: 0)

post '/push' do
  content_type :json

  request.body.rewind
  payload_body = request.body.read

  unless verify_signature(payload_body)
    $log.warn("Signatures didn't match!")
    return halt 500, { 'msg' => "Signatures didn't match!" }.to_json
  end

  push = JSON.parse(payload_body)
  $log.info("Received JSON: #{push.inspect}")
  qlen = redis.lpush(QUEUENAME, Time.now.getutc) if 'refs/heads/master' == push['ref']

  { 'msg' => 'OK', 'queue_length' => qlen }.to_json
end

get '*' do
  content_type :json

  $log.warn("Unwanted request: #{request.path_info}")
  { 'msg' => 'What do you want? Nothing here...' }.to_json
end

def verify_signature(payload_body)
  signature = 'sha1=' + OpenSSL::HMAC.hexdigest(OpenSSL::Digest.new('sha1'), ENV['GITHUB_TOKEN'], payload_body)
  Rack::Utils.secure_compare(signature, request.env['HTTP_X_HUB_SIGNATURE'])
end

