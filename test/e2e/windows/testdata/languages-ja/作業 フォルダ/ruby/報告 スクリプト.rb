env = ENV
puts "event=#{env['KICKD_EVENT']}"
puts "trigger=#{env['KICKD_TRIGGER']}"
puts "msg=#{env['KICKD_DATA_MSG']}"
puts "dir=#{Dir.pwd}"
puts 'payload=' + File.read(env['KICKD_PAYLOAD_FILE'], encoding: 'UTF-8')
warn 'to stderr'
exit Integer(env['KICKD_DATA_CODE'])
