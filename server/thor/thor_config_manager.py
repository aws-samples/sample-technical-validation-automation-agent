#!/usr/bin/env python3
"""
PSA Valid Configuration Manager
Handles loading and managing user configuration
"""

import yaml
from pathlib import Path
import os
import shutil


class ThorConfig:
    """Configuration manager for Thor CLI"""
    
    # Default config location - organized in .thor_cli directory
    DEFAULT_CONFIG_PATH = Path.home() / '.thor_cli' / 'config.yaml'
    
    # Default configuration values
    DEFAULTS = {
        'directories': {
            'partner_folders': str(Path.home() / 'Documents' / 'PSA_Validations'),
            'validation_tools': str(Path.home() / '.thor_cli' / 'tools')
        },
        'aws': {
            's3_profile': '',
            'bedrock_profile': '',
            's3_bucket': '',
            'region': 'us-east-1'
        },
        'validation': {
            'default_controls': [],
            'application_type': 'SOFTWARE',
            'system_prompt': 'revised'
        },
        'behavior': {
            'auto_organize_files': True,
            'use_newest_excel': True,
            'generate_summary': True,
            'verbose': True
        },
        'advanced': {
            'api_retry_count': 3,
            'download_timeout': 300
        }
    }
    
    def __init__(self, config_path=None):
        """Initialize config manager"""
        self.config_path = Path(config_path) if config_path else self.DEFAULT_CONFIG_PATH
        self.config = self.load_config()
        
    def load_config(self):
        """Load configuration from file or use defaults"""
        if self.config_path.exists():
            try:
                with open(self.config_path, 'r') as f:
                    user_config = yaml.safe_load(f)
                    # Merge with defaults (user values override defaults)
                    return self._merge_configs(self.DEFAULTS.copy(), user_config or {})
            except Exception as e:
                print(f"Warning: Could not load config from {self.config_path}: {e}")
                print("Using default configuration")
                return self.DEFAULTS.copy()
        else:
            # Use defaults if no config file exists
            return self.DEFAULTS.copy()
    
    def _merge_configs(self, base, override):
        """Recursively merge override config into base config"""
        for key, value in override.items():
            if key in base and isinstance(base[key], dict) and isinstance(value, dict):
                base[key] = self._merge_configs(base[key], value)
            else:
                base[key] = value
        return base
    
    def get(self, *keys, default=None):
        """Get nested config value"""
        value = self.config
        for key in keys:
            if isinstance(value, dict):
                value = value.get(key)
                if value is None:
                    return default
            else:
                return default
        return value
    
    def get_partner_folders_dir(self):
        """Get partner folders directory, expanding ~ to home"""
        path_str = self.get('directories', 'partner_folders', default=str(Path.cwd()))
        return Path(os.path.expanduser(path_str))
    
    def get_validation_tools_dir(self):
        """Get validation tools directory, expanding ~ to home"""
        path_str = self.get('directories', 'validation_tools', 
                           default=str(Path.home() / 'Downloads' / 'Archive (1)'))
        return Path(os.path.expanduser(path_str))
    
    def get_s3_profile(self):
        """Get S3 AWS profile"""
        return self.get('aws', 's3_profile', default='')
    
    def get_bedrock_profile(self):
        """Get Bedrock AWS profile"""
        return self.get('aws', 'bedrock_profile', default='')
    
    def get_s3_bucket(self):
        """Get S3 bucket name"""
        return self.get('aws', 's3_bucket', default='')
    
    def create_default_config(self, force=False):
        """Create default config file in user's home directory"""
        if self.config_path.exists() and not force:
            return False, f"Config already exists at {self.config_path}"
        
        # Copy template config to user's home
        template_path = Path(__file__).parent / 'psa_config.yaml'
        
        if template_path.exists():
            shutil.copy(template_path, self.config_path)
            return True, f"Config created at {self.config_path}"
        else:
            # Create from defaults
            with open(self.config_path, 'w') as f:
                yaml.dump(self.DEFAULTS, f, default_flow_style=False, sort_keys=False)
            return True, f"Default config created at {self.config_path}"
    
    def show_config(self):
        """Display current configuration"""
        return yaml.dump(self.config, default_flow_style=False, sort_keys=False)
    
    def get_config_path(self):
        """Get path to config file"""
        return self.config_path
    
    def config_exists(self):
        """Check if config file exists"""
        return self.config_path.exists()


# Singleton instance
_config_instance = None

def get_config(config_path=None):
    """Get or create config instance"""
    global _config_instance
    if _config_instance is None or config_path is not None:
        _config_instance = ThorConfig(config_path)
    return _config_instance

# Alias for backwards compatibility
PSAConfig = ThorConfig
LokiConfig = ThorConfig
