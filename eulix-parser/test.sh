#!/bin/bash
# Build and test the Eulix parser

set -e

echo "🚀 Building Eulix Parser..."
echo

# Check if we're in the parser directory
if [ ! -f "Cargo.toml" ]; then
    if [ -d "parser" ]; then
        cd parser
    else
        echo " Error: Not in parser directory and parser/ not found"
        exit 1
    fi
fi

# Build the parser
echo "🔨 Building Rust parser..."
cargo build --release

if [ $? -eq 0 ]; then
    echo "✅ Parser built successfully!"
    echo "📍 Binary location: target/release/eulix-parser"
else
    echo " Build failed!"
    exit 1
fi

echo
echo "📊 Binary size:"
ls -lh target/release/eulix-parser

# Create test project
echo
echo "📁 Creating test project..."
cd ..
mkdir -p test_project/src/auth
mkdir -p test_project/src/models
mkdir -p test_project/src/utils

# Create sample Python file 1
cat > test_project/src/auth/login.py << 'EOF'
"""
Authentication module for handling user login.
"""

from typing import Optional
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select

router = APIRouter()

# TODO: Add rate limiting
# TODO: Implement 2FA support
MAX_LOGIN_ATTEMPTS: int = 5
SESSION_TIMEOUT: int = 3600


class LoginRequest:
    """Request model for user login."""
    username: str
    password: str
    remember_me: bool = False


def authenticate_user(username: str, password: str) -> Optional[object]:
    """
    Authenticate user credentials against database.

    Args:
        username: The username to authenticate
        password: The plain text password

    Returns:
        User object if authentication successful, None otherwise
    """
    hashed = hash_password(password)
    user = query_user(username)

    if user and verify_password(password, user.password_hash):
        return user

    return None


@router.post("/login")
async def login_endpoint(request: LoginRequest):
    """
    Handle user login requests.

    This endpoint validates credentials and generates a JWT token.
    """
    user = authenticate_user(request.username, request.password)

    if not user:
        raise HTTPException(status_code=401, detail="Invalid credentials")

    token = create_access_token(user.id)
    return {"token": token, "user_id": user.id}


def create_access_token(user_id: int) -> str:
    """Generate a JWT access token for the user."""
    payload = build_payload(user_id)
    return encode_token(payload)
EOF

# Create sample Python file 2
cat > test_project/src/models/user.py << 'EOF'
"""User model definitions."""

from sqlalchemy import Column, Integer, String, Boolean
from datetime import datetime


class User:
    """
    User database model.

    Represents a user in the system with authentication credentials.
    """

    id: int
    username: str
    email: str
    password_hash: str
    is_active: bool = True
    created_at: datetime

    def __init__(self, username: str, email: str):
        """Initialize a new user."""
        self.username = username
        self.email = email
        self.is_active = True

    def check_password(self, password: str) -> bool:
        """Check if provided password matches user's password."""
        return verify_hash(password, self.password_hash)

    def set_password(self, password: str):
        """Set user password with hashing."""
        self.password_hash = hash_password(password)

    def __repr__(self):
        return f"<User {self.username}>"
EOF

# Create sample Python file 3
cat > test_project/src/utils/security.py << 'EOF'
"""Security utility functions."""

import hashlib
import secrets
from typing import Optional

# TODO: Use bcrypt instead of SHA256
SECRET_KEY = "your-secret-key-here"


def hash_password(password: str) -> str:
    """
    Hash a password using SHA256.

    Args:
        password: Plain text password

    Returns:
        Hashed password string
    """
    salt = secrets.token_hex(16)
    combined = f"{password}{salt}{SECRET_KEY}"
    return hashlib.sha256(combined.encode()).hexdigest()


def verify_password(password: str, hash_value: str) -> bool:
    """
    Verify a password against its hash.

    Args:
        password: Plain text password to verify
        hash_value: Hashed password to compare against

    Returns:
        True if password matches, False otherwise
    """
    return hash_password(password) == hash_value


def generate_token(user_id: int, expires_in: int = 3600) -> str:
    """Generate a secure token for user session."""
    data = f"{user_id}:{secrets.token_hex(32)}:{expires_in}"
    return hashlib.sha256(data.encode()).hexdigest()
EOF

# Create requirements.txt
cat > test_project/requirements.txt << 'EOF'
fastapi==0.104.0
sqlalchemy==2.0.23
pydantic==2.5.0
uvicorn==0.24.0
python-jose==3.3.0
passlib==1.7.4
EOF

echo "✅ Test project created"
echo

# Run parser
echo "⚙️  Running parser on test project..."
echo
./parser/target/release/eulix-parser \
    --root test_project \
    --output test_project/knowledge_base.json \
    --verbose

echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✨ Build and test complete!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo
echo "📄 Knowledge base generated at:"
echo "   test_project/knowledge_base.json"
echo
echo "🔍 Inspect the output:"
echo "   cat test_project/knowledge_base.json | jq '.metadata'"
echo "   cat test_project/knowledge_base.json | jq '.structure | keys'"
echo "   cat test_project/knowledge_base.json | jq '.dependency_graph.nodes | length'"
echo
echo "📊 View specific file:"
echo "   cat test_project/knowledge_base.json | jq '.structure[\"test_project/src/auth/login.py\"]'"
