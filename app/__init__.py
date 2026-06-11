from flask import Flask
from app.routes import vfs_blueprint

app = Flask(__name__)
app.register_blueprint(vfs_blueprint)