#!/usr/bin/env python3
"""
Loki Validator - Built-in validation engine

Extracted from PSA_PROMPT_HELPER.py to be integrated into Loki CLI.
Provides all validation capabilities without external dependencies.
"""

import boto3
import uuid
import csv
import os
import subprocess
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor, as_completed

try:
    import PyPDF2
    PYPDF2_AVAILABLE = True
except ImportError:
    PYPDF2_AVAILABLE = False

try:
    from PIL import Image
    import io
    PILLOW_AVAILABLE = True
except ImportError:
    PILLOW_AVAILABLE = False


class ThorValidator:
    """
    Core validation engine for Thor CLI
    
    Provides the same functionality as PSA_PROMPT_HELPER.py but integrated
    into Thor CLI for self-contained operation.
    """
    
    def __init__(self, model_id="global.anthropic.claude-sonnet-4-5-20250929-v1:0", 
                 aws_profile=None, prompts_dir=None):
        """
        Initialize validator with Bedrock client
        
        Args:
            model_id: Bedrock model ID to use
            aws_profile: AWS profile name for Bedrock access
            prompts_dir: Directory containing prompt files
        """
        # Store config for lazy credential loading — fresh session on every API call
        self.aws_profile = aws_profile or os.environ.get('AWS_PROFILE', None)
        self.model_id = model_id
        self.prompts_dir = Path(prompts_dir) if prompts_dir else self._get_default_prompts_dir()
        
        # Cache for resized images (prevents re-processing)
        self.image_cache = {}
        
        print(f"LOG: Initialized validator with model: {model_id}")
        print(f"LOG: Prompts directory: {self.prompts_dir}")

    def _get_bedrock_client(self):
        """Create a fresh boto3 Bedrock client, with smart credential resolution."""
        if self.aws_profile:
            session = boto3.Session(profile_name=self.aws_profile)
            return session.client('bedrock-runtime')
        
        # Try to find a profile with credential_process (auto-refreshing creds)
        cp_profile = self._find_credential_process_profile()
        if cp_profile:
            try:
                session = boto3.Session(profile_name=cp_profile)
                return session.client('bedrock-runtime')
            except Exception:
                pass
        
        # Default boto3 chain
        session = boto3.Session()
        return session.client('bedrock-runtime')

    @staticmethod
    def _find_credential_process_profile():
        """Find the first AWS config profile that uses credential_process."""
        import configparser
        config_path = Path.home() / ".aws" / "config"
        if not config_path.exists():
            return None
        config = configparser.ConfigParser()
        config.read(str(config_path))
        if config.has_option("default", "credential_process"):
            return None  # boto3 default chain handles this
        for section in config.sections():
            if config.has_option(section, "credential_process"):
                profile_name = section.replace("profile ", "") if section.startswith("profile ") else section
                return profile_name
        return None
    
    def _get_default_prompts_dir(self):
        """Get default prompts directory"""
        # Try user's Thor CLI prompts first
        user_prompts = Path.home() / '.thor_cli' / 'prompts'
        if user_prompts.exists():
            return user_prompts
        
        # Fallback to config
        try:
            from thor_config_manager import get_config
            return get_config().get_validation_tools_dir()
        except:
            # Last resort
            return Path.home() / 'Downloads' / 'Archive (1)'
    
    def _validate_doc006_marketplace(self, partner_folder):
        """
        Special validation for DOC-006 with Marketplace URL checking
        
        Validates DOC-006 by:
        1. Checking if partner mentions Marketplace
        2. If URL provided, verifies it's accessible
        3. Returns appropriate result
        """
        import re
        
        partner_folder = Path(partner_folder)
        csv_file = partner_folder / 'partner_responses.csv'
        
        # Get partner response
        partner_response = ""
        with open(csv_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if row['controlId'] == 'DOC-006':
                    partner_response = row['partner_response']
                    break
        
        if not partner_response:
            return "YES. No mention of AWS Marketplace (optional requirement)"
        
        response_lower = partner_response.lower()
        
        # Check if states N/A or not applicable
        if 'n/a' in response_lower or 'not applicable' in response_lower or 'not available' in response_lower:
            print(f"Partner states: Not on AWS Marketplace")
            return "YES. Partner confirms solution is not on AWS Marketplace (which is acceptable)"
        
        # Extract Marketplace URLs
        marketplace_pattern = r'https?://(?:www\.)?aws\.amazon\.com/marketplace/pp/[^\s,\)"\']+'
        urls = re.findall(marketplace_pattern, partner_response)
        
        if not urls:
            # No URL found and no mention of Marketplace
            if 'marketplace' not in response_lower:
                print(f"No mention of AWS Marketplace")
                return "YES. No Marketplace mention (optional requirement)"
            else:
                # Mentions Marketplace but no URL
                print(f"⚠️  Mentions Marketplace but no URL provided")
                return "NO. Partner mentions AWS Marketplace but does not provide URL"
        
        # URL found - validate it
        url = urls[0]
        print(f"Found Marketplace URL: {url}")
        print(f"   Checking URL accessibility...")
        
        try:
            import urllib.request
            import urllib.error
            
            # Check URL with HEAD request
            req = urllib.request.Request(url, method='HEAD')
            req.add_header('User-Agent', 'Thor-CLI/1.0')
            
            try:
                with urllib.request.urlopen(req, timeout=10) as response:
                    status = response.status
                    
                    if status == 200:
                        print(f"   ✅ URL accessible (HTTP {status})")
                        return f"YES. AWS Marketplace listing provided and accessible: {url}"
                    else:
                        print(f"   ⚠️  URL returned HTTP {status}")
                        return f"NO. AWS Marketplace URL provided but returned HTTP {status}: {url}"
                        
            except urllib.error.HTTPError as e:
                print(f"   ❌ URL not accessible (HTTP {e.code})")
                if e.code == 404:
                    return f"NO. AWS Marketplace URL provided but listing not found (404): {url}"
                else:
                    return f"NO. AWS Marketplace URL provided but not accessible (HTTP {e.code}): {url}"
                    
            except urllib.error.URLError as e:
                print(f"   ❌ URL error: {e.reason}")
                return f"NO. AWS Marketplace URL provided but not accessible: {url}"
                
        except Exception as e:
            print(f"   ⚠️  Could not check URL: {e}")
            # If URL check fails, fall back to AI validation
            return f"YES. AWS Marketplace URL provided (URL verification failed - manual check recommended): {url}"
    
    def _auto_split_large_pdfs(self, supporting_docs_dir):
        """
        Automatically split PDFs over 80 pages into 40-page chunks.
        This allows faster processing and better batching.
        
        Args:
            supporting_docs_dir: Path to supporting_docs folder
        """
        if not PYPDF2_AVAILABLE:
            return  # Skip if PyPDF2 not installed
        
        # Check for qpdf availability
        try:
            subprocess.run(['which', 'qpdf'], capture_output=True, check=True)
        except:
            print("   ⚠️  qpdf not found - skipping auto-split (install: brew install qpdf)")
            return
        
        for pdf_file in list(supporting_docs_dir.glob('*.pdf')):
            # Skip already-split files
            if 'Part' in pdf_file.stem and any(c.isdigit() for c in pdf_file.stem.split('Part')[-1]):
                continue
            
            try:
                # Count pages
                with open(pdf_file, 'rb') as f:
                    reader = PyPDF2.PdfReader(f)
                    pages = len(reader.pages)
                
                # Only split if over 80 pages
                if pages > 80:
                    print(f"   📄 Auto-splitting {pdf_file.name} ({pages} pages → 40-page chunks)...")
                    
                    # Calculate number of parts (40 pages each)
                    num_parts = (pages + 39) // 40
                    
                    for part_num in range(num_parts):
                        start_page = part_num * 40 + 1
                        end_page = min((part_num + 1) * 40, pages)
                        
                        output_file = supporting_docs_dir / f"{pdf_file.stem}_Part{part_num + 1}.pdf"
                        
                        # Use qpdf to split
                        subprocess.run([
                            'qpdf', str(pdf_file),
                            '--pages', '.', f'{start_page}-{end_page}', '--',
                            str(output_file)
                        ], check=True, capture_output=True)
                    
                    # Remove original large file
                    pdf_file.unlink()
                    print(f"      Created {num_parts} parts (original removed)")
                    
            except Exception as e:
                print(f"   ⚠️  Could not split {pdf_file.name}: {e}")
    
    def load_prompts(self, control_id, system_mode='revised', detailed_questions=False):
        """
        Load prompts for validation
        
        Args:
            control_id: Control ID to validate
            system_mode: 'old', 'new', or 'revised'
            detailed_questions: Use detailed vs simple questions
            
        Returns:
            Dictionary with system_prompt, prompt_context, prompt_question
        """
        # Read system prompt from text file
        system_file = self.prompts_dir / f'system_{system_mode}.txt'
        if not system_file.exists():
            raise FileNotFoundError(f"System prompt not found: {system_file}")
        
        with open(system_file, 'r', encoding='utf-8') as f:
            system_prompt = f.read().strip()
        
        # Load context from CSV
        context_file = self.prompts_dir / 'CONTEXT.csv'
        if not context_file.exists():
            raise FileNotFoundError(f"Context file not found: {context_file}")
        
        context_data = {}
        with open(context_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                context_data[row['controlId']] = row['prompt_context']
        
        if control_id not in context_data:
            raise ValueError(f"Control {control_id} not found in context file")
        
        # Select question style
        if detailed_questions:
            question = (
                "Based on the details provided, provide response whether this offering is approved or not. "
                "The response must include: Decision: Is the control response meeting the requirement? "
                "Is the offering approved or not approved based on the criteria for passing in the "
                "calibration guideline for this control? use YES or NO Reason: Reason for the decision, "
                "What was the offering missing? Any recommendation? Make it a simple response, with as "
                "little details as possible. Add here all the things partner needs to modify in order to "
                "pass this control. Add any identifiers or documents that cause the NO. EVALUATION GUIDANCE: "
                "Approve if the partner demonstrates they meet the fundamental requirement objective, even "
                "if using different approaches than the specific examples mentioned. Focus on outcomes and "
                "capabilities, not specific tool names."
            )
        else:
            question = "Based on the details provided, is this offering approved?"
        
        return {
            'controlId': control_id,
            'system_prompt': system_prompt,
            'prompt_context': context_data[control_id],
            'prompt_question': question
        }
    
    def _resize_image_if_needed(self, image_path, image_bytes, extension):
        """Resize image if dimensions exceed 8000px (with caching)"""
        if not PILLOW_AVAILABLE:
            return image_bytes
        
        # Check cache first
        cache_key = f"{image_path}_{extension}"
        if cache_key in self.image_cache:
            return self.image_cache[cache_key]
        
        try:
            img = Image.open(io.BytesIO(image_bytes))
            width, height = img.size
            
            max_dim = 8000
            if width <= max_dim and height <= max_dim:
                self.image_cache[cache_key] = image_bytes
                return image_bytes  # No resize needed
            
            # Calculate new size maintaining aspect ratio
            if width > height:
                new_width = max_dim
                new_height = int((max_dim / width) * height)
            else:
                new_height = max_dim
                new_width = int((max_dim / height) * width)
            
            print(f"      🔄 Resizing image {width}x{height} → {new_width}x{new_height}")
            
            # Resize with high quality
            img = img.resize((new_width, new_height), Image.Resampling.LANCZOS)
            
            # Save to bytes
            output = io.BytesIO()
            img_format = 'JPEG' if extension in ['jpg', 'jpeg'] else 'PNG'
            img.save(output, format=img_format, quality=95)
            
            resized_bytes = output.getvalue()
            self.image_cache[cache_key] = resized_bytes
            return resized_bytes
            
        except Exception as e:
            print(f"      ⚠️  Could not resize image: {e}")
            self.image_cache[cache_key] = image_bytes
            return image_bytes
    
    def process_files(self, file_paths, max_doc_bytes=4_500_000):
        """Process files for Bedrock submission, handling oversized files."""
        processed_files = []
        for file_path in file_paths:
            path = Path(file_path)
            extension = path.suffix.lower().lstrip('.')
            
            # Map jpg to jpeg for Bedrock
            if extension == 'jpg':
                extension = 'jpeg'
            
            # Use UUID for Bedrock naming
            clean_name = str(uuid.uuid4()).replace('-', '')
            
            with open(file_path, 'rb') as f:
                file_bytes = f.read()
            
            if not file_bytes:
                print(f"   ⚠️  Skipping empty file: {path.name}")
                continue
            
            # Auto-resize images if needed (Bedrock limit: 8000px per dimension)
            if extension in ['png', 'jpg', 'jpeg']:
                file_bytes = self._resize_image_if_needed(file_path, file_bytes, extension)
            
            # Handle oversized documents by extracting text
            if len(file_bytes) > max_doc_bytes:
                print(f"   📄 File too large ({len(file_bytes)/1024/1024:.1f}MB): {path.name} — extracting text...")
                extracted_text = self._extract_text_from_file(file_path, extension, max_doc_bytes)
                if extracted_text:
                    # Send as a text/plain document instead
                    text_bytes = extracted_text.encode('utf-8')[:max_doc_bytes]
                    processed_files.append((text_bytes, 'txt', clean_name))
                    continue
                else:
                    print(f"   ⚠️  Could not extract text from {path.name}, skipping")
                    continue
            
            processed_files.append((file_bytes, extension, clean_name))
        
        return processed_files

    def _extract_text_from_file(self, file_path, extension, max_bytes=4_500_000):
        """Extract text content from oversized files."""
        try:
            if extension == 'pdf':
                return self._extract_pdf_text(file_path, max_bytes)
            elif extension in ('pptx',):
                return self._extract_pptx_text(file_path, max_bytes)
            elif extension in ('docx',):
                return self._extract_docx_text(file_path, max_bytes)
            elif extension in ('xlsx', 'xls'):
                import pandas as pd
                df = pd.read_excel(file_path)
                return df.to_string()[:max_bytes]
            else:
                # Try reading as text
                with open(file_path, 'r', encoding='utf-8', errors='ignore') as f:
                    return f.read(max_bytes)
        except Exception as e:
            print(f"   ⚠️  Text extraction failed for {Path(file_path).name}: {e}")
            return None

    def _extract_pdf_text(self, file_path, max_bytes=4_500_000):
        """Extract text from PDF using PyPDF2."""
        try:
            with open(file_path, 'rb') as f:
                reader = PyPDF2.PdfReader(f)
                text_parts = []
                for page in reader.pages:
                    text = page.extract_text()
                    if text:
                        text_parts.append(text)
                    if sum(len(t) for t in text_parts) > max_bytes:
                        break
                return "\n".join(text_parts)[:max_bytes]
        except Exception as e:
            print(f"   ⚠️  PDF text extraction failed: {e}")
            return None

    def _extract_pptx_text(self, file_path, max_bytes=4_500_000):
        """Extract text from PowerPoint files."""
        try:
            from zipfile import ZipFile
            import defusedxml.ElementTree as ET
            
            text_parts = []
            with ZipFile(file_path, 'r') as z:
                # Get slide files
                slide_files = sorted([f for f in z.namelist() if f.startswith('ppt/slides/slide') and f.endswith('.xml')])
                for slide_file in slide_files:
                    with z.open(slide_file) as sf:
                        tree = ET.parse(sf)
                        # Extract all text elements
                        for elem in tree.iter():
                            if elem.text and elem.text.strip():
                                text_parts.append(elem.text.strip())
                    if sum(len(t) for t in text_parts) > max_bytes:
                        break
            return "\n".join(text_parts)[:max_bytes]
        except Exception as e:
            print(f"   ⚠️  PPTX text extraction failed: {e}")
            return None

    def _extract_docx_text(self, file_path, max_bytes=4_500_000):
        """Extract text from Word documents."""
        try:
            from zipfile import ZipFile
            import defusedxml.ElementTree as ET
            
            text_parts = []
            with ZipFile(file_path, 'r') as z:
                with z.open('word/document.xml') as doc:
                    tree = ET.parse(doc)
                    for elem in tree.iter():
                        if elem.text and elem.text.strip():
                            text_parts.append(elem.text.strip())
            return "\n".join(text_parts)[:max_bytes]
        except Exception as e:
            print(f"   ⚠️  DOCX text extraction failed: {e}")
            return None
    
    def invoke_bedrock(self, documents=None, images=None, text=None, 
                      prompt_context="", prompt_question="", system_prompt="", cache_bust=False):
        """Invoke Bedrock with files and prompts
        
        Args:
            cache_bust: If True, adds unique string to prevent prompt caching (for consensus mode)
        """
        content_blocks = []
        
        if prompt_context:
            content_blocks.append({"text": prompt_context})
        
        if text:
            content_blocks.append({"text": text})
        
        # Cache-busting for consensus mode: add unique comment
        if cache_bust:
            import uuid
            content_blocks.append({"text": f"\n<!-- Cache-bust: {uuid.uuid4()} -->"})
        
        if images:
            for img_bytes, img_format in images:
                content_blocks.append({
                    "image": {
                        "format": img_format,
                        "source": {"bytes": img_bytes}
                    }
                })
        
        if documents:
            for doc_bytes, doc_format, doc_name in documents:
                content_blocks.append({
                    "document": {
                        "format": doc_format,
                        "name": doc_name,
                        "source": {"bytes": doc_bytes}
                    }
                })
        
        if prompt_question:
            content_blocks.append({"text": prompt_question})
        
        # Bedrock automatically caches system prompts and content when repeated
        # No explicit cachePoint needed - caching is automatic based on content matching
        request = {
            "modelId": self.model_id,
            "messages": [{"role": "user", "content": content_blocks}],
            "system": [{"text": system_prompt}],
            "inferenceConfig": {"maxTokens": 2000, "temperature": 0.0},
        }
        
        response = self._get_bedrock_client().converse(**request)
        
        # Print cache info
        usage = response.get('usage', {})
        print(f"LOG: Tokens - Input: {usage.get('inputTokens', 0)}, Output: {usage.get('outputTokens', 0)}")
        
        return response['output']['message']['content'][0]['text']
    
    def validate_control(self, control_id, partner_folder, system_mode='revised', 
                        detailed_questions=False, cache_bust=False):
        """
        Validate a single control
        
        Args:
            control_id: Control ID to validate
            partner_folder: Path to partner folder
            system_mode: 'old', 'new', or 'revised'
            detailed_questions: Use detailed vs simple questions
            cache_bust: If True, adds unique string to prevent Bedrock caching (for consensus)
            
        Returns:
            Validation result string
        """
        print(f"\n🎯 VALIDATING {control_id}")
        print(f"   System: {system_mode}")
        print(f"   Questions: {'Detailed' if detailed_questions else 'Simple'}")
        print("=" * 50)
        
        # Special validation for DOC-006 (Marketplace URL checking)
        if control_id == 'DOC-006':
            return self._validate_doc006_marketplace(partner_folder)
        
        # Load prompts
        try:
            prompt_data = self.load_prompts(control_id, system_mode, detailed_questions)
        except Exception as e:
            print(f"❌ ERROR: {e}")
            return None
        
        # Get partner response
        partner_folder = Path(partner_folder)
        csv_file = partner_folder / 'partner_responses.csv'
        
        if not csv_file.exists():
            raise FileNotFoundError(f"partner_responses.csv not found in {partner_folder}")
        
        partner_response = ""
        with open(csv_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                if row['controlId'] == control_id:
                    partner_response = row['partner_response']
                    break
        
        if not partner_response:
            print(f"⚠️  No partner response for {control_id}")
            return "No partner response found"
        
        print(f"Partner response: {partner_response[:100]}...")
        
        # Get supporting documents
        supporting_docs = partner_folder / 'supporting_docs'
        all_files = []
        
        if supporting_docs.exists():
            all_files = [str(p) for p in supporting_docs.glob('*') 
                        if p.is_file() and not p.name.startswith('~$')]
        
        # Separate by type
        doc_exts = {'.pdf', '.xlsx', '.xls', '.docx', '.doc', '.csv', '.html', '.txt', '.md'}
        img_exts = {'.png', '.jpg', '.jpeg'}
        
        documents = [f for f in all_files if Path(f).suffix.lower() in doc_exts]
        images = [f for f in all_files if Path(f).suffix.lower() in img_exts]
        
        # Separate large PDFs from normal files for smart batching
        large_pdfs = []
        normal_docs = []
        
        for doc in documents:
            if Path(doc).suffix.lower() == '.pdf':
                # Estimate if PDF is "large" (> 50 pages likely means problem in batches)
                # Use file size as proxy: 1 page ≈ 50KB average
                file_size = Path(doc).stat().st_size
                estimated_pages = file_size / 50000  # Rough estimate
                
                if estimated_pages > 50 or 'Part1' in doc or 'Part2' in doc:
                    # Likely large or split PDF - process individually
                    large_pdfs.append(doc)
                else:
                    normal_docs.append(doc)
            else:
                normal_docs.append(doc)
        
        # Add images to normal processing
        normal_docs.extend(images)
        
        # Smart token-aware batching
        batch_size = 5  # Default
        
        # Estimate total file size and adjust batch size accordingly
        if normal_docs:
            total_size = sum(Path(f).stat().st_size for f in normal_docs)
            avg_size = total_size / len(normal_docs) if normal_docs else 0
            
            # Heuristic: 1KB file ≈ 100 tokens (more accurate than 300)
            # Safe limit: 150K tokens for documents (leave room for prompts/context)
            estimated_tokens_per_file = (avg_size / 1024) * 100
            
            # Calculate safe batch size
            if estimated_tokens_per_file > 50000:  # Very large files
                batch_size = 1
                print(f"⚠️  Large files detected - using batch_size=1")
            elif estimated_tokens_per_file > 30000:  # Large files  
                batch_size = 2
                print(f"⚡ Large files - using batch_size=2")
            elif estimated_tokens_per_file > 15000:  # Medium files
                batch_size = 3
                print(f"⚡ Medium files - using batch_size=3")
            else:  # Small files (diagrams, etc.)
                batch_size = 5
                print(f"⚡ Small files - using batch_size=5 (fast!)")
            
            print(f"   Processing {len(normal_docs)} file(s) in batches of {batch_size}...")
            all_docs = normal_docs
        elif large_pdfs:
            all_docs = []
            batch_size = 1  # Process large PDFs individually
        else:
            all_docs = [None]
        
        for i in range(0, len(all_docs) if all_docs[0] else 1, batch_size):
            batch = all_docs[i:i+batch_size] if all_docs[0] else []
            
            batch_docs = [f for f in batch if f and Path(f).suffix.lower() in doc_exts] if batch else []
            batch_images = [f for f in batch if f and Path(f).suffix.lower() in img_exts] if batch else []
            
            # Process files
            processed_docs = None
            processed_images = None
            
            if batch_docs:
                doc_data = self.process_files(batch_docs)
                processed_docs = [(b, fmt, name) for b, fmt, name in doc_data]
            
            if batch_images:
                img_data = self.process_files(batch_images)
                processed_images = [(b, fmt) for b, fmt, _ in img_data]
            
            # Invoke Bedrock (no retry needed - large PDFs already separated)
            text = f"Partner Response from self-assessment:\nResponse for {control_id} submitted by Partner: {partner_response}"
            
            result = self.invoke_bedrock(
                documents=processed_docs,
                images=processed_images,
                text=text,
                prompt_context=prompt_data['prompt_context'],
                prompt_question=prompt_data['prompt_question'],
                system_prompt=prompt_data['system_prompt'],
                cache_bust=cache_bust
            )
            
            print(result)
            
            if result.startswith("YES"):
                print(f"\n✅ PASSED!")
                return result
        
        # If didn't pass with normal docs, try large PDFs as LAST RESORT
        if large_pdfs and normal_docs:
            print(f"\n📄 Trying {len(large_pdfs)} large PDF(s) individually as fallback...")
            for pdf in large_pdfs:
                processed_docs = [(b, fmt, name) for b, fmt, name in self.process_files([pdf])]
                
                text = f"Partner Response from self-assessment:\nResponse for {control_id} submitted by Partner: {partner_response}"
                
                result = self.invoke_bedrock(
                    documents=processed_docs,
                    images=None,
                    text=text,
                    prompt_context=prompt_data['prompt_context'],
                    prompt_question=prompt_data['prompt_question'],
                    system_prompt=prompt_data['system_prompt'],
                    cache_bust=cache_bust
                )
                
                print(result)
                
                if result.startswith("YES"):
                    print(f"\n✅ PASSED!")
                    return result
        
        print(f"\n❌ FAILED")
        return result
    
    def validate_batch(self, control_ids, partner_folder, system_mode='revised',
                      detailed_questions=False, consensus_runs=1, cache_bust=False):
        """
        Validate multiple controls IN PARALLEL (like Loki backend)
        
        Args:
            control_ids: List of control IDs
            partner_folder: Path to partner folder
            system_mode: 'old', 'new', or 'revised'
            detailed_questions: Use detailed vs simple questions
            consensus_runs: Number of times to run each control (1=normal, 3=consensus)
            cache_bust: If True, prevents Bedrock prompt caching (for consensus independence)
            
        Returns:
            Dictionary of results keyed by control ID
        """
        # If consensus requested, run multiple times and vote
        if consensus_runs > 1:
            return self._validate_with_consensus(control_ids, partner_folder, system_mode,
                                                 detailed_questions, consensus_runs)
        print(f"🚀 PARALLEL BATCH VALIDATION")
        print(f"⚡ Processing {len(control_ids)} control(s) concurrently...")
        print(f"🎯 System: {system_mode}")
        print(f"📋 Questions: {'Detailed' if detailed_questions else 'Simple'}")
        print("=" * 50)
        
        # Auto-split large PDFs BEFORE validation (improves speed & batching)
        partner_folder = Path(partner_folder)
        supporting_docs = partner_folder / 'supporting_docs'
        if supporting_docs.exists():
            self._auto_split_large_pdfs(supporting_docs)
        
        results = {}
        passed = 0
        
        # Use ThreadPoolExecutor like Loki backend (max_workers=15)
        with ThreadPoolExecutor(max_workers=min(15, len(control_ids))) as executor:
            # Submit all controls at once (with cache_bust if enabled)
            future_to_control = {
                executor.submit(self.validate_control, control_id, partner_folder, 
                              system_mode, detailed_questions, cache_bust): control_id
                for control_id in control_ids
            }
            
            # Collect results as they complete (not in order)
            completed = 0
            for future in as_completed(future_to_control):
                control_id = future_to_control[future]
                completed += 1
                
                try:
                    result = future.result()
                    results[control_id] = result
                    
                    if result and result.startswith("YES"):
                        print(f"\n✅ [{completed}/{len(control_ids)}] {control_id}: PASSED")
                        passed += 1
                    else:
                        print(f"\n❌ [{completed}/{len(control_ids)}] {control_id}: FAILED")
                        
                except Exception as e:
                    print(f"\n❌ [{completed}/{len(control_ids)}] {control_id}: ERROR - {str(e)}")
                    results[control_id] = f"Error: {str(e)}"
        
        # Generate timestamped summary report
        self._generate_validation_report(control_ids, results, passed, partner_folder)
        
        # Summary
        print("\n" + "="*50)
        print("📊 SUMMARY:")
        print(f"   ✅ Passed: {passed}/{len(control_ids)}")
        print(f"   ⚡ Parallel execution with max_workers=15 (Loki method)")
        print("="*50)
        
        return results
    
    def _validate_with_consensus(self, control_ids, partner_folder, system_mode, 
                                detailed_questions, runs=3):
        """
        Validate controls multiple times and use majority vote
        
        This improves consistency by running each control N times and taking
        the majority result (e.g., 2/3 pass = PASS)
        """
        print(f"🔄 CONSENSUS MODE: Running {runs}x for consistency")
        print(f"   Taking majority vote (e.g., 2/{runs} pass = PASS)")
        print("="*70 + "\n")
        
        all_runs = []
        
        # Create consensus results folder
        from datetime import datetime
        partner_folder = Path(partner_folder)
        consensus_dir = partner_folder / 'reports' / 'consensus'
        timestamp = datetime.now().strftime('%Y%m%d_%H%M%S')
        run_dir = consensus_dir / f'run_set_{timestamp}'
        run_dir.mkdir(parents=True, exist_ok=True)
        
        # Run validation N times (with early stop if all pass 2/2)
        for run_num in range(runs):
            print(f"\n{'='*70}")
            print(f"🔄 RUN {run_num + 1}/{runs}")
            print(f"   🔓 Cache-busting enabled (each run is independent)")
            print(f"{'='*70}")
            
            # Run normal batch validation
            # Always enable cache-busting in consensus mode for truly independent evaluations
            results = self.validate_batch(control_ids, partner_folder, system_mode,
                                         detailed_questions, consensus_runs=1, 
                                         cache_bust=True)
            all_runs.append(results)
            
            # Save this run's results to file
            run_file = run_dir / f'run_{run_num + 1}_results.json'
            import json
            run_data = {
                'run_number': run_num + 1,
                'timestamp': datetime.now().isoformat(),
                'results': {
                    control_id: {
                        'result': result,
                        'passed': result.startswith('YES') if result else False
                    }
                    for control_id, result in results.items()
                }
            }
            with open(run_file, 'w') as f:
                json.dump(run_data, f, indent=2)
            print(f"   💾 Run {run_num + 1} results saved: {run_file.name}")
            
            # Early stop check after run 2 (if all controls pass 2/2, skip run 3)
            if run_num == 1 and runs == 3:
                all_passed_twice = True
                for control_id in control_ids:
                    votes = []
                    for run_results in all_runs:
                        result = run_results.get(control_id, "")
                        if result is None:
                            votes.append(False)
                        else:
                            votes.append(result.startswith("YES"))
                    
                    if not all(votes):
                        all_passed_twice = False
                        break
                
                if all_passed_twice:
                    print(f"\n{'='*70}")
                    print(f"✅ EARLY STOP: All controls passed 2/2 runs")
                    print(f"   No need for run 3 - consensus reached!")
                    print(f"{'='*70}")
                    break
        
        # Calculate consensus
        print(f"\n{'='*70}")
        print(f"📊 CALCULATING CONSENSUS ({runs} runs)")
        print(f"{'='*70}\n")
        
        final_results = {}
        passed = 0
        variance_controls = []
        
        for control_id in control_ids:
            votes = []
            detailed_reasons = []
            
            for run_results in all_runs:
                result = run_results.get(control_id, "")
                if result is None:
                    is_yes = False
                    detailed_reasons.append("Error: No result")
                else:
                    is_yes = result and result.startswith("YES")
                    detailed_reasons.append(result)
                votes.append(is_yes)
            
            yes_count = sum(votes)
            no_count = len(all_runs) - yes_count
            
            # Majority vote
            consensus_pass = yes_count > no_count
            
            # Track variance
            if yes_count > 0 and no_count > 0:
                variance_controls.append(f"{control_id} ({yes_count}Y/{no_count}N)")
            
            # Build detailed result with consensus + actual reasons
            if consensus_pass:
                passing_reasons = [r for r, v in zip(detailed_reasons, votes) if v]
                reason_text = passing_reasons[0] if passing_reasons else "Passed"
                final_results[control_id] = f"YES. Consensus {yes_count}/{runs}.\n\nRepresentative reason:\n{reason_text}"
                print(f"✅ {control_id}: PASS (consensus {yes_count}/{runs})")
                passed += 1
            else:
                failing_reasons = [r for r, v in zip(detailed_reasons, votes) if not v]
                reason_text = failing_reasons[0] if failing_reasons else "Failed"
                final_results[control_id] = f"NO. Consensus {no_count}/{runs}.\n\nRepresentative reason:\n{reason_text}"
                print(f"❌ {control_id}: FAIL (consensus {no_count}/{runs})")
        
        # Save variance analysis report
        if variance_controls:
            self._generate_variance_report(control_ids, all_runs, variance_controls, run_dir)
        
        # Show variance report
        if variance_controls:
            print(f"\n{'='*70}")
            print(f"⚠️  VARIANCE DETECTED ({len(variance_controls)} controls)")
            print(f"{'='*70}")
            for ctrl in variance_controls:
                print(f"   {ctrl}")
            print(f"\nThese controls gave inconsistent results across {runs} runs")
        else:
            print(f"\n✅ All controls showed 100% consistency across {runs} runs!")
        
        # Generate report
        self._generate_validation_report(control_ids, final_results, passed, Path(partner_folder))
        
        # Summary
        print("\n" + "="*70)
        print("📊 CONSENSUS SUMMARY:")
        print(f"   ✅ Passed: {passed}/{len(control_ids)}")
        print(f"   🔄 Consensus: {runs} runs per control")
        if variance_controls:
            print(f"   ⚠️  Variance: {len(variance_controls)} controls inconsistent")
        print("="*70)
        
        return final_results
    
    def _generate_variance_report(self, control_ids, all_runs, variance_controls_list, run_dir):
        """Generate detailed variance analysis report"""
        from datetime import datetime
        import json
        
        lines = []
        lines.append("# Consensus Variance Analysis")
        lines.append("")
        lines.append(f"**Generated**: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
        lines.append(f"**Total Runs**: {len(all_runs)}")
        lines.append(f"**Variance Controls**: {len(variance_controls_list)}")
        lines.append("")
        lines.append("## Controls with Inconsistent Results")
        lines.append("")
        
        for variance_str in variance_controls_list:
            # Extract control ID from "CONTROL-ID (2Y/1N)" format
            control_id = variance_str.split(' ')[0]
            lines.append(f"### {variance_str}")
            lines.append("")
            
            # Show results from each run
            for run_num, run_results in enumerate(all_runs, 1):
                result = run_results.get(control_id, "")
                is_pass = result.startswith('YES') if result else False
                status_icon = '✅ PASS' if is_pass else '❌ FAIL'
                
                lines.append(f"**Run {run_num}:** {status_icon}")
                lines.append("")
                
                # Extract reason (first 300 chars)
                if result:
                    reason = result.replace('YES. ', '').replace('NO. ', '').strip()
                    lines.append(f"> {reason[:300]}{'...' if len(reason) > 300 else ''}")
                    lines.append("")
            
            lines.append("**Analysis:**")
            lines.append("")
            lines.append("This control showed variance, meaning Bedrock gave different assessments across runs. ")
            lines.append("Possible reasons:")
            lines.append("- Borderline case (close to pass/fail threshold)")
            lines.append("- Ambiguous partner response (could be interpreted multiple ways)")
            lines.append("- Complex requirement with multiple criteria")
            lines.append("- Temperature=0 still has some inherent LLM variance")
            lines.append("")
            lines.append("**Recommendation:** PSA should manually review this control to make final determination.")
            lines.append("")
            lines.append("---")
            lines.append("")
        
        # Add summary
        lines.append("## Summary")
        lines.append("")
        lines.append(f"Variance indicates controls that are close to the pass/fail boundary. ")
        lines.append(f"These {len(variance_controls_list)} control(s) should be prioritized for PSA manual review ")
        lines.append(f"when comparing with Loki results.")
        lines.append("")
        
        # Save report
        variance_file = run_dir / 'variance_analysis.md'
        with open(variance_file, 'w') as f:
            f.write('\n'.join(lines))
        
        print(f"   📊 Variance analysis: {variance_file}")
    
    def _generate_validation_report(self, control_ids, results, passed, partner_folder):
        """Generate timestamped validation report in reports/summary/ folder + root copy"""
        from datetime import datetime
        
        # Create reports/summary directory
        partner_folder = Path(partner_folder)
        reports_dir = partner_folder / 'reports'
        summary_dir = reports_dir / 'summary'
        summary_dir.mkdir(parents=True, exist_ok=True)
        
        # Generate timestamps
        timestamp_display = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        timestamp_file = datetime.now().strftime('%Y%m%d_%H%M%S')
        
        # Calculate pass rate
        pass_rate = (passed / len(control_ids) * 100) if control_ids else 0
        
        # Build markdown report (match psa_automation_workflow format)
        lines = []
        lines.append("# Validation Summary")
        lines.append("")
        lines.append(f"**Partner**: {partner_folder.name}")
        lines.append(f"**Application ID**: N/A")
        lines.append(f"**Timestamp**: {timestamp_display}")
        lines.append(f"**Ticket**: N/A")
        lines.append("")
        lines.append("## Results Overview")
        lines.append(f"- **Total Controls**: {len(control_ids)}")
        lines.append(f"- **✅ Passed**: {passed} ({pass_rate:.1f}%)")
        lines.append(f"- **❌ Failed**: {len(control_ids) - passed} ({100-pass_rate:.1f}%)")
        lines.append("")
        
        # Passed controls with reasons
        passed_controls = [cid for cid, result in results.items() 
                          if result and result.startswith("YES")]
        if passed_controls:
            lines.append("## Passed Controls")
            lines.append("")
            for control in passed_controls:
                result_text = results.get(control, "")
                if result_text:
                    # Extract reason (remove YES. prefix)
                    reason = result_text.replace("YES. ", "").strip()
                    lines.append(f"### ✅ {control}")
                    lines.append("")
                    lines.append("**Reason**:")
                    lines.append("```")
                    lines.append(reason[:1000])  # First 1000 chars
                    lines.append("```")
                    lines.append("")
                else:
                    lines.append(f"- ✅ {control}")
            lines.append("")
        
        # Failed controls with reasons
        failed_controls = [cid for cid, result in results.items() 
                          if not (result and result.startswith("YES"))]
        if failed_controls:
            lines.append("## Failed Controls")
            lines.append("")
            for control in failed_controls:
                lines.append(f"### ❌ {control}")
                result_text = results.get(control, "")
                if result_text:
                    # Extract reason (remove YES/NO prefix)
                    reason = result_text.replace("YES. ", "").replace("NO. ", "").strip()
                    
                    lines.append("")
                    lines.append("**Reason**:")
                    lines.append("```")
                    lines.append(reason[:1000])  # First 1000 chars
                    lines.append("```")
                    lines.append("")
        
        lines.append("---")
        lines.append("*Generated by Thor CLI*")
        
        report_content = '\n'.join(lines)
        
        # Save to reports/summary/ with timestamp
        archived_report = summary_dir / f'validation_summary_{timestamp_file}.md'
        with open(archived_report, 'w') as f:
            f.write(report_content)
        
        # Also save to root for backward compatibility (diff command expects this)
        root_summary = partner_folder / 'validation_summary.md'
        with open(root_summary, 'w') as f:
            f.write(report_content)
        
        print(f"\n📄 Report saved:")
        print(f"   Root: {root_summary}")
        print(f"   Archived: {archived_report}")
