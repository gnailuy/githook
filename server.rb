require 'json'
require 'sinatra'

set :bind, '0.0.0.0'
set :port, 20182

post '/push' do
  request.body.rewind
  payload_body = request.body.read
  verify_signature(payload_body)
  push = JSON.parse(payload_body)
  puts "Received JSON: #{push.inspect}"
  system('sh update.sh >> logs/update.log 2>&1') if "refs/heads/master" == push['ref']
end

get '*' do
  "What do you want? Nothing here..."
end

def verify_signature(payload_body)
  signature = 'sha1=' + OpenSSL::HMAC.hexdigest(OpenSSL::Digest.new('sha1'), ENV['GITHUB_TOKEN'], payload_body)
  return halt 500, "Signatures didn't match!" unless Rack::Utils.secure_compare(signature, request.env['HTTP_X_HUB_SIGNATURE'])
end

