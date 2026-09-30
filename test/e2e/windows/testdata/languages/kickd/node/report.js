const fs = require('node:fs');

const env = process.env;
console.log('event=' + env.KICKD_EVENT);
console.log('trigger=' + env.KICKD_TRIGGER);
console.log('msg=' + env.KICKD_DATA_MSG);
console.log('dir=' + process.cwd());
console.log('payload=' + fs.readFileSync(env.KICKD_PAYLOAD_FILE, 'utf8'));
console.error('to stderr');
process.exitCode = Number(env.KICKD_DATA_CODE);
