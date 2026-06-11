from app import db

class Session(db.Model):
    id = db.Column(db.Integer, primary_key=True)
    vfs_entries = db.relationship('VirtualFsEntry', backref='session', lazy=True)

class VirtualFsEntry(db.Model):
    id = db.Column(db.Integer, primary_key=True)
    session_id = db.Column(db.Integer, db.ForeignKey('session.id'), nullable=False)
    target_path = db.Column(db.String(255), nullable=False)
    status = db.Column(db.String(50), nullable=False, default='PROPOSED')