'use strict';
console.log('Hello from lacron!');
console.log(`time=${new Date().toISOString()}`);
console.log(`node=${process.version} lts=${process.release.lts || 'none'}`);
console.log(`uid=${process.getuid()} gid=${process.getgid()}`);
