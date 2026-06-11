const fs = require('fs');
const path = require('path');
const sqlite3 = require('sqlite3').verbose();

class Database {
  constructor(dbPath) {
    this.dbPath = dbPath;
    this.db = new sqlite3.Database(dbPath);
    this.applicationId = 'wander-sort';
  }

  open() {
    return new Promise((resolve, reject) => {
      this.db.get('SELECT value FROM metadata WHERE key = ?', 'application_id', (err, row) => {
        if (err) {
          reject(err);
        } else if (row && row.value !== this.applicationId) {
          reject(new Error('Invalid application ID'));
        } else {
          resolve();
        }
      });
    });
  }

  // ... rest of the class remains the same ...
}

module.exports = Database;