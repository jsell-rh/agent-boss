#!/usr/bin/env python3
"""
Agent Boss Coordination Client for Paude Containers

Handles communication between Claude Code agents and Agent Boss server.
Provides status updates, ignition handling, and git commit hooks.
"""

import json
import os
import sys
import time
import requests
from datetime import datetime
from typing import Optional, Dict, Any

class CoordinationClient:
    def __init__(self):
        self.boss_url = os.getenv('BOSS_URL', 'http://localhost:8899')
        self.workspace = os.getenv('WORKSPACE_NAME', 'default')
        self.agent_name = os.getenv('AGENT_NAME', 'agent')
        self.agent_role = os.getenv('AGENT_ROLE', 'development')
        self.source_files = os.getenv('SOURCE_FILES', '')
        self.session = requests.Session()
        self.session.headers.update({
            'Content-Type': 'application/json',
            'X-Agent-Name': self.agent_name
        })
        
    def post_status(self, status: str, summary: str, **kwargs) -> bool:
        """Post agent status update to Agent Boss"""
        try:
            update = {
                'status': status,
                'summary': summary,
                'repo_url': 'git@gitlab.cee.redhat.com:ocm/agent-boss.git',
                'session_id': os.getenv('TMUX_SESSION', ''),
                **kwargs
            }
            
            url = f"{self.boss_url}/spaces/{self.workspace}/agent/{self.agent_name}"
            response = self.session.post(url, json=update, timeout=10)
            
            if response.status_code in [200, 201, 202]:
                print(f"✅ Status posted: {status} - {summary}")
                return True
            else:
                print(f"❌ Status post failed: {response.status_code} - {response.text}")
                return False
                
        except Exception as e:
            print(f"❌ Status post error: {e}")
            return False
    
    def get_ignition(self) -> Optional[str]:
        """Get agent ignition context from Agent Boss"""
        try:
            url = f"{self.boss_url}/spaces/{self.workspace}/ignition/{self.agent_name}"
            params = {'session_id': os.getenv('TMUX_SESSION', '')}
            
            response = self.session.get(url, params=params, timeout=10)
            
            if response.status_code == 200:
                print("✅ Ignition context retrieved")
                return response.text
            else:
                print(f"❌ Ignition failed: {response.status_code}")
                return None
                
        except Exception as e:
            print(f"❌ Ignition error: {e}")
            return None
    
    def get_blackboard(self) -> Optional[str]:
        """Get current workspace blackboard"""
        try:
            url = f"{self.boss_url}/spaces/{self.workspace}/raw"
            response = self.session.get(url, timeout=10)
            
            if response.status_code == 200:
                print("✅ Blackboard retrieved")
                return response.text
            else:
                print(f"❌ Blackboard failed: {response.status_code}")
                return None
                
        except Exception as e:
            print(f"❌ Blackboard error: {e}")
            return None
    
    def post_git_commit(self, commit_hash: str, message: str) -> bool:
        """Post git commit notification"""
        summary = f"{self.agent_name}: committed {commit_hash[:8]} - {message[:50]}..."
        return self.post_status('active', summary, 
                               items=[f"Git commit: {commit_hash}", f"Message: {message}"])
    
    def register_agent(self) -> bool:
        """Register agent with initial status"""
        summary = f"{self.agent_name}: Paude container initialized ({self.agent_role})"
        items = [
            f"Role: {self.agent_role}",
            f"Source focus: {self.source_files}",
            f"Container: Paude + Claude Code",
            f"Workspace: {self.workspace}"
        ]
        
        return self.post_status('idle', summary, items=items)
    
    def check_messages(self) -> list:
        """Check for incoming messages from Boss/other agents"""
        try:
            response = self.session.get(f"{self.boss_url}/spaces/{self.workspace}/agent/{self.agent_name}")
            if response.status_code == 200:
                agent_data = response.json()
                messages = agent_data.get('messages', [])
                return [msg for msg in messages if not msg.get('read', False)]
            else:
                print(f"❌ Message check failed: {response.status_code}")
                return []
                
        except Exception as e:
            print(f"❌ Message check error: {e}")
            return []
    
    def send_boss_reply(self, original_message_id: str, reply_text: str) -> bool:
        """Send a ?BOSS reply to a message"""
        try:
            # Mark the message as read/replied by posting a question
            reply_summary = f"{self.agent_name}: [?BOSS] {reply_text}"
            
            # Post as a question that will appear in the Inbox
            return self.post_status('active', reply_summary, 
                                    questions=[reply_text],
                                    items=[f"Replying to message: {original_message_id}"])
                                    
        except Exception as e:
            print(f"❌ Boss reply error: {e}")
            return False

def main():
    """CLI interface for coordination client"""
    if len(sys.argv) < 2:
        print("Usage: coordination-client.py <command> [args...]")
        print("Commands:")
        print("  register                    - Register agent with boss")
        print("  status <status> <summary>   - Post status update")
        print("  ignition                    - Get ignition context")
        print("  blackboard                  - Get current blackboard")
        print("  git-commit <hash> <msg>     - Post git commit")
        print("  check-messages              - Check for incoming messages")
        print("  boss-reply <msg_id> <text>  - Reply to message with ?BOSS")
        sys.exit(1)
    
    client = CoordinationClient()
    command = sys.argv[1]
    
    if command == 'register':
        success = client.register_agent()
        sys.exit(0 if success else 1)
        
    elif command == 'status':
        if len(sys.argv) < 4:
            print("Usage: coordination-client.py status <status> <summary>")
            sys.exit(1)
        success = client.post_status(sys.argv[2], sys.argv[3])
        sys.exit(0 if success else 1)
        
    elif command == 'ignition':
        context = client.get_ignition()
        if context:
            print(context)
            sys.exit(0)
        else:
            sys.exit(1)
            
    elif command == 'blackboard':
        board = client.get_blackboard()
        if board:
            print(board)
            sys.exit(0)
        else:
            sys.exit(1)
            
    elif command == 'git-commit':
        if len(sys.argv) < 4:
            print("Usage: coordination-client.py git-commit <hash> <message>")
            sys.exit(1)
        success = client.post_git_commit(sys.argv[2], ' '.join(sys.argv[3:]))
        sys.exit(0 if success else 1)
        
    elif command == 'check-messages':
        messages = client.check_messages()
        if messages:
            print(f"📧 {len(messages)} new messages:")
            for msg in messages:
                print(f"  [{msg['id']}] From {msg['sender']}: {msg['message']}")
                print(f"      Time: {msg['timestamp']}")
        else:
            print("📬 No new messages")
        sys.exit(0)
        
    elif command == 'boss-reply':
        if len(sys.argv) < 4:
            print("Usage: coordination-client.py boss-reply <message_id> <reply_text>")
            sys.exit(1)
        message_id = sys.argv[2]
        reply_text = ' '.join(sys.argv[3:])
        success = client.send_boss_reply(message_id, reply_text)
        sys.exit(0 if success else 1)
        
    else:
        print(f"Unknown command: {command}")
        sys.exit(1)

if __name__ == '__main__':
    main()