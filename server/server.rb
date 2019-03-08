require 'json'
require 'logger'
require 'redis'
require 'sinatra'

set :bind, '0.0.0.0'
set :port, 20182

$log = Logger.new('githook_server.log', 0, 100 * 1024 * 1024)
$log.level = Logger::WARN

redis = Redis.new(host: 'redis', port: 6379, db: 0)

post '/push' do
  request.body.rewind
  payload_body = request.body.read
  verify_signature(payload_body)
  push = JSON.parse(payload_body)
  $log.info('Received JSON: #{push.inspect}')
  qlen = redis.lpush('githook', Time.now.getutc) if 'refs/heads/master' == push['ref']

  content_type :json
  { 'msg' => 'OK', 'queue_length' => qlen }.to_json
end

get '*' do
  $log.warn('Unwanted request: #{@env["sinatra.route"]}')
  content_type :json
  { 'msg' => 'What do you want? Nothing here...' }.to_json
end

def verify_signature(payload_body)
  signature = 'sha1=' + OpenSSL::HMAC.hexdigest(OpenSSL::Digest.new('sha1'), ENV['GITHUB_TOKEN'], payload_body)
  return halt 500, "Signatures didn't match!" unless Rack::Utils.secure_compare(signature, request.env['HTTP_X_HUB_SIGNATURE'])
end

