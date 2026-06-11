const Scanner = require('./scanner');

async function main() {
  const outputDir = './output';
  const scanner = new Scanner(outputDir);
  await scanner.rescan();
}

main();