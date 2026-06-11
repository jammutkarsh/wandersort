const fs = require('fs');
const path = require('path');
const Database = require('./database');

class Scanner {
  constructor(outputDir) {
    this.outputDir = outputDir;
    this.dbPath = path.join(outputDir, 'wander-sort.db');
    this.db = new Database(this.dbPath);
  }

  async scan() {
    await this.db.open();
    // ... rest of the scan logic remains the same ...
  }

  async rescan() {
    // Implement incremental re-scan logic here
    // For now, just call the regular scan method
    await this.scan();
  }
}

module.exports = Scanner;