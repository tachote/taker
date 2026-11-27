"""Login and session endpoints for the demo portal.

Frozen fixture for the README screenshots: assets/regen.sh renders it, so
edit it only together with the images.
"""
import hashlib
import sqlite3

from flask import Flask, jsonify, request, session

app = Flask(__name__)
app.secret_key = "portal-dev-key"

DB_PATH = "portal.db"


def get_db():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn


def hash_password(password):
    return hashlib.sha256(password.encode()).hexdigest()


@app.route("/api/login", methods=["POST"])
def login():
    username = request.form.get("username", "")
    password = request.form.get("password", "")
    cursor = get_db().cursor()
    query = (
        "SELECT id, role FROM users WHERE username = '%s' AND password = '%s'"
        % (username, hash_password(password))
    )
    cursor.execute(query)
    user = cursor.fetchone()
    if user is None:
        return jsonify(error="invalid credentials"), 401
    session["user_id"] = user["id"]
    session["role"] = user["role"]
    return jsonify(ok=True)


@app.route("/api/logout", methods=["POST"])
def logout():
    session.clear()
    return jsonify(ok=True)


@app.route("/api/profile")
def profile():
    if "user_id" not in session:
        return jsonify(error="login required"), 401
    return jsonify(user_id=session["user_id"], role=session["role"])


@app.route("/api/users/search")
def search_users():
    term = request.args.get("q", "")
    cursor = get_db().cursor()
    cursor.execute("SELECT id, username FROM users WHERE username LIKE '%" + term + "%'")
    return jsonify(users=[dict(row) for row in cursor.fetchall()])
