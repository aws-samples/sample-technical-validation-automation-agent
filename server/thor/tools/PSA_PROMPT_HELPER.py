#!/usr/bin/env python3
import boto3
import uuid
import csv
import argparse
import sys
from pathlib import Path

class BedrockInvoker:
    def __init__(self, model_id="global.anthropic.claude-sonnet-4-5-20250929-v1:0"):
        session = boto3.Session()
        self.client = session.client('bedrock-runtime')
        self.model_id = model_id
        print(f"LOG: Initialized with model_id: {model_id}")
    
    def load_prompts(self, system_file, control_id, detailed_questions=False):
        """ULTRA Simple: Read system prompt from text file + context from CSV + hardcoded questions"""
        
        # Read system prompt from simple text file (no more CSV bloat!)
        with open(system_file, 'r', encoding='utf-8') as f:
            system_prompt = f.read().strip()
        
        # Load context (evaluation criteria - only CSV we actually need)
        context_data = {}
        with open('CONTEXT_old.csv', 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for row in reader:
                context_data[row['controlId']] = row['prompt_context']
        
        # Simple hardcoded question selection
        if detailed_questions:
            question = "Based on the details provided, provide response whether this offering is approved or not. The response must include: Decision: Is the control response meeting the requirement? Is the offering approved or not approved based on the criteria for passing in the calibration guideline for this control? use YES or NO Reason: Reason for the decision, What was the offering missing? Any recommendation? Make it a simple response, with as little details as possible. Add here all the things partner needs to modify in order to pass this control. Add any identifiers or documents that cause the NO. EVALUATION GUIDANCE: Approve if the partner demonstrates they meet the fundamental requirement objective, even if using different approaches than the specific examples mentioned. Focus on outcomes and capabilities, not specific tool names."
        else:
            question = "Based on the details provided, is this offering approved?"
        
        if control_id not in context_data:
            raise ValueError(f"Control {control_id} not found in context file")
        
        return {
            'controlId': control_id,
            'system_prompt': system_prompt,  # Same prompt for all controls!
            'prompt_context': context_data[control_id],
            'prompt_question': question
        }

    def load_prompts_from_csv(self, csv_file, start_row, end_row):
        """Load prompts from CSV file for rows start_row to end_row (1-indexed, inclusive)"""
        prompts = []
        with open(csv_file, 'r', encoding='utf-8') as f:
            reader = csv.DictReader(f)
            for i, row in enumerate(reader, 1):
                if start_row <= i <= end_row:
                    prompts.append({
                        'controlId': row['controlId'],
                        'system_prompt': row['system_prompt'],
                        'prompt_context': row['prompt_context'],
                        'prompt_question': row['prompt_question']
                    })
        return prompts
    
    def process_files(self, file_paths):
        """Auxiliary method to extract format, bytes, and names from file paths"""
        processed_files = []
        for file_path in file_paths:
            path = Path(file_path)
            extension = path.suffix.lower().lstrip('.')
            
            # Map jpg to jpeg for Bedrock compatibility
            if extension == 'jpg':
                extension = 'jpeg'
                
            original_name = path.name
            
            # Use UUID without hyphens and no extension for Bedrock
            clean_name = str(uuid.uuid4()).replace('-', '')
            print(f"LOG: File name switch: {original_name} -> {clean_name}")
            
            with open(file_path, 'rb') as f:
                file_bytes = f.read()
            
            processed_files.append((file_bytes, extension, clean_name))
        return processed_files
    
    def invoke_with_user_input(self, text=None, document_paths=None, image_paths=None, prompt_context="", prompt_question="", system_prompt=""):
        """Parent method for user to provide text and file paths"""
        print(f"LOG: User inputs - text: {text}, document_paths: {document_paths}, image_paths: {image_paths}")
        
        documents = None
        images = None
        
        if document_paths:
            doc_data = self.process_files(document_paths)
            documents = [(doc_bytes, doc_format, doc_name) for doc_bytes, doc_format, doc_name in doc_data]
        
        if image_paths:
            img_data = self.process_files(image_paths)
            images = [(img_bytes, img_format) for img_bytes, img_format, _ in img_data]
        
        print("LOG: Calling Bedrock invoke...")
        result = self.invoke_bedrock_with_multiple_files(documents, images, text, prompt_context, prompt_question, system_prompt)
        print(f"LOG: Result: {result}")
        return result
    
    def get_tool_specification(self):
        """Tool specification for structured audit responses"""
        BEDROCK_RESPONSE_YES = "YES"
        BEDROCK_RESPONSE_NO = "NO"
        INSUFFICIENT_EVIDENCE_PROVIDED = "INSUFFICIENT_EVIDENCE_PROVIDED"
        CONTROL_REQUIREMENTS_NOT_MET = "CONTROL_REQUIREMENTS_NOT_MET"
        
        response_schema = {
            "type": "object",
            "properties": {
                "result": {
                    "type": "string",
                    "description": "Indicates if the input passes the control requirements.",
                    "enum": [BEDROCK_RESPONSE_YES, BEDROCK_RESPONSE_NO]
                },
                "errors": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "errorCode": {
                                "type": "string",
                                "description": "Use 'INSUFFICIENT_EVIDENCE_PROVIDED' if evidence exists but it's not comprehensive enough to prove compliance. Use 'CONTROL_REQUIREMENTS_NOT_MET' if the submitted evidence does not meet the requirements.",
                                "enum": [INSUFFICIENT_EVIDENCE_PROVIDED, CONTROL_REQUIREMENTS_NOT_MET]
                            },
                            "reason": {
                                "type": "string",
                                "description": "Give reasoning for the selected errorCode",
                                "minLength": 0,
                                "maxLength": 500
                            },
                            "subResources": {
                                "type": "array",
                                "description": "List of specific subresources (e.g., case study IDs, file names) that are failing this error.",
                                "items": {
                                    "type": "string",
                                    "description": "Identifier of the failing subresource"
                                }
                            }
                        }
                    }
                }
            },
            "required": ["result", "errors"]
        }
        
        return {
            "toolSpec": {
                "name": "audit_response",
                "description": "Provide audit response in structured format.",
                "inputSchema": {
                    "json": response_schema
                }
            }
        }

    def invoke_bedrock_with_multiple_files(self, documents=None, images=None, text=None, prompt_context="", prompt_question="", system_prompt=""):
        content_blocks = []
        
        if prompt_context:
            content_blocks.append({"text": prompt_context})
        
        if text:
            content_blocks.append({"text": text})
        
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
        
        request = {
            "modelId": self.model_id,
            "messages": [{"role": "user", "content": content_blocks}],
            "system": [{"text": system_prompt}],
            "inferenceConfig": {"maxTokens": 2000, "temperature": 0.0},
        }
        
        response = self.client.converse(**request)
        
        # Print cache information
        usage = response.get('usage', {})
        print(f"LOG: Cache Info - CacheReadInputTokens: {usage.get('inputTokens', 0)}")
        print(f"LOG: Cache Info - CacheWriteInputTokens: {usage.get('outputTokens', 0)}")
        
        # Extract tool use response
        return response['output']['message']['content'][0]['text']
    
    def extract_tool_response(self, response):
        """Extract structured response from tool use"""
        for content_block in response.get('output', {}).get('message', {}).get('content', []):
            if 'toolUse' in content_block:
                tool_input = content_block['toolUse']['input']
                result = tool_input.get('result', 'NO')
                
                if result == 'NO':
                    errors = tool_input.get('errors', [])
                    formatted_errors = []
                    for i, error in enumerate(errors):
                        error_info = {
                            "code": error.get('errorCode', 'MISSING_ERROR_CODE'),
                            "message": error.get('reason', 'Missing reason'),
                            "subResources": error.get('subResources', [])
                        }
                        formatted_errors.append(error_info)
                    
                    return {
                        "Decision": result,
                        "Errors": formatted_errors
                    }
                else:
                    return {
                        "Decision": result,
                        "Reason": "Partner meets the requirements"
                    }
        
        # Fallback to original text response
        return response['output']['message']['content'][0]['text']

    def run_audit(self, control_id, system_file, detailed_questions=False):
        """Enhanced audit runner with batch processing for additional evidence"""
        print(f"\n🎯 AUDITING {control_id}")
        print(f"   System: {system_file}")
        print(f"   Questions: {'Detailed' if detailed_questions else 'Simple'}")
        print("=" * 50)
        
        # Load prompt data
        try:
            prompt_data = self.load_prompts(system_file, control_id, detailed_questions)
        except Exception as e:
            print(f"❌ ERROR: {e}")
            return None
        
        # Get partner response
        partner_response = ""
        try:
            with open('partner_responses.csv', 'r', encoding='utf-8') as f:
                reader = csv.DictReader(f)
                for row in reader:
                    if row['controlId'] == control_id:
                        partner_response = row['partner_response']
                        break
        except:
            pass
        
        if not partner_response:
            print(f"WARNING: No partner response found for {control_id}")
            return "No partner response found"
        
        print(f"LOG: Partner response: {partner_response[:100]}...")
        
        # 🆕 BATCH PROCESSING for additional evidence documents
        folder_path = Path("Cerbrec")
        all_files = [str(p) for p in folder_path.glob("**/*") if p.is_file() and not p.name.startswith('~$')]
        
        # Separate documents and images
        doc_extensions = {'.pdf', '.xlsx', '.xls', '.docx', '.doc', '.csv', '.html', '.txt', '.md'}
        img_extensions = {'.png', '.jpg', '.jpeg'}
        
        documents = [f for f in all_files if Path(f).suffix.lower() in doc_extensions]
        images = [f for f in all_files if Path(f).suffix.lower() in img_extensions]
        
        all_docs = documents + images  # Combine for batching
        
        if not all_docs:
            print("LOG: No additional evidence documents found in redis/")
            # Process without additional documents
            all_docs = [None]  # Single pass without docs
        
        batch_size = 5
        all_results = []
        
        for i in range(0, len(all_docs), batch_size):
            batch = all_docs[i:i+batch_size] if all_docs else []
            batch_num = (i // batch_size) + 1
            
            # Separate batch into docs and images
            batch_docs = [f for f in batch if Path(f).suffix.lower() in doc_extensions]
            batch_images = [f for f in batch if Path(f).suffix.lower() in img_extensions]
            
            if batch:
                print(f"\n--- BATCH {batch_num} ({len(batch)} files: {len(batch_docs)} docs, {len(batch_images)} images) ---")
            else:
                print(f"\n--- Processing without additional documents ---")
            
            result = self.invoke_with_user_input(
                text=f"Partner Response from self-assessment:\nResponse for {control_id} submitted by Partner: {partner_response}",
                document_paths=batch_docs if batch_docs else None,
                image_paths=batch_images if batch_images else None,
                prompt_context=prompt_data['prompt_context'],
                prompt_question=prompt_data['prompt_question'],
                system_prompt=prompt_data['system_prompt']
            )
            
            print(result)
            all_results.append(result)
            
            if result.startswith("YES"):
                if batch:
                    print(f"\n✅ PASSED! Found sufficient evidence in batch {batch_num}")
                else:
                    print(f"\n✅ PASSED! Based on partner response")
                return result
        
        # If we get here, all batches failed
        print(f"\n❌ FAILED after reviewing all evidence")
        return all_results[-1] if all_results else "No evidence found"

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="🚀 ACTUALLY Simple PSA Helper - Only What You Need!",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
🎯 SIMPLE EXAMPLES:
  python PSA_PROMPT_HELPER.py DOC-001-SERVICE                          # Default (revised system + simple questions)
  python PSA_PROMPT_HELPER.py --old DOC-001-SERVICE                    # Forgiving system + detailed questions
  python PSA_PROMPT_HELPER.py --old-system --detailed-questions DOC-001-SERVICE   # Mix: old system + detailed questions
        """
    )
    
    parser.add_argument('controls', nargs='+', help='Control IDs to audit')
    
    # Simple presets
    parser.add_argument('--old', action='store_true', help='Use old (forgiving) system prompts + detailed questions')
    parser.add_argument('--new', action='store_true', help='Use new (strict) system prompts + simple questions')
    parser.add_argument('--revised', action='store_true', help='Use revised (balanced) system prompts + simple questions')
    
    # Simple mixing
    parser.add_argument('--old-system', action='store_true', help='Use old system prompts')
    parser.add_argument('--new-system', action='store_true', help='Use new system prompts')  
    parser.add_argument('--revised-system', action='store_true', help='Use revised system prompts')
    parser.add_argument('--detailed-questions', action='store_true', help='Use detailed questions instead of simple')
    parser.add_argument('--model', '-m', default='global.anthropic.claude-sonnet-4-5-20250929-v1:0', help='Bedrock model ID')
    
    args = parser.parse_args()
    
    # Pick system file (simple text files - easy to edit!)
    if args.old or args.old_system:
        system_file = 'system_old.txt'
        preset = "OLD (Forgiving)"
    elif args.new or args.new_system:
        system_file = 'system_new.txt'
        preset = "NEW (Strict)"
    elif args.revised or args.revised_system:
        system_file = 'system_revised.txt'
        preset = "REVISED (Balanced)"
    else:
        system_file = 'system_revised.txt'  # Default
        preset = "DEFAULT (Revised)"
    
    # Question format (only other thing that changes)
    detailed_questions = args.detailed_questions or args.old  # OLD preset uses detailed by default
    
    print("🚀 ACTUALLY SIMPLE PSA HELPER")
    print("=============================")
    print(f"✨ Processing {len(args.controls)} control(s)")
    print(f"🎯 System: {preset}")
    print(f"📋 Questions: {'Detailed' if detailed_questions else 'Simple'}")
    print("=" * 50)
    
    invoker = BedrockInvoker(model_id=args.model)
    
    # Process controls
    results = {}
    for i, control_id in enumerate(args.controls, 1):
        print(f"\n🔍 [{i}/{len(args.controls)}] {control_id}")
        result = invoker.run_audit(control_id, system_file, detailed_questions)
        results[control_id] = result
        
        if result and result.startswith("YES"):
            print(f"✅ {control_id}: PASSED")
        else:
            print(f"❌ {control_id}: FAILED")
    
    # Summary
    print("\n" + "="*50)
    print("📊 SUMMARY:")
    passed = sum(1 for r in results.values() if isinstance(r, str) and r.startswith("YES"))
    print(f"   ✅ Passed: {passed}/{len(results)}")
    print("="*50)
