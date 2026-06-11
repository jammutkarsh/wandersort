from flask import Blueprint, request, jsonify
from app.models import Session, VirtualFsEntry

vfs_blueprint = Blueprint('vfs_blueprint', __name__)

@vfs_blueprint.route('/sessions/<int:session_id>/vfs', methods=['GET'])
def get_vfs(session_id):
    session = Session.query.get(session_id)
    if session is None:
        return jsonify({'error': 'Session not found'}), 404
    
    vfs_entries = VirtualFsEntry.query.filter_by(session_id=session_id, status='PROPOSED').all()
    vfs_tree = build_vfs_tree(vfs_entries)
    return jsonify(vfs_tree)

@vfs_blueprint.route('/sessions/<int:session_id>/vfs/confirm', methods=['POST'])
def confirm_vfs(session_id):
    session = Session.query.get(session_id)
    if session is None:
        return jsonify({'error': 'Session not found'}), 404
    
    vfs_tree = request.get_json()
    vfs_entries = VirtualFsEntry.query.filter_by(session_id=session_id, status='PROPOSED').all()
    updated_vfs_entries = reconcile_vfs_tree(vfs_tree, vfs_entries)
    for entry in updated_vfs_entries:
        entry.status = 'APPROVED'
    return jsonify({'message': 'VFS confirmed successfully'})

def build_vfs_tree(vfs_entries):
    vfs_tree = {}
    for entry in vfs_entries:
        path_parts = entry.target_path.split('/')
        current_node = vfs_tree
        for part in path_parts[:-1]:
            if part not in current_node:
                current_node[part] = {}
            current_node = current_node[part]
        current_node[path_parts[-1]] = {}
    return vfs_tree

def reconcile_vfs_tree(vfs_tree, vfs_entries):
    updated_vfs_entries = []
    for entry in vfs_entries:
        path_parts = entry.target_path.split('/')
        current_node = vfs_tree
        for part in path_parts[:-1]:
            if part not in current_node:
                current_node[part] = {}
            current_node = current_node[part]
        if path_parts[-1] not in current_node:
            # Handle rename or removal
            new_name = list(current_node.keys())[0]
            entry.target_path = '/'.join(path_parts[:-1] + [new_name])
        updated_vfs_entries.append(entry)
    return updated_vfs_entries