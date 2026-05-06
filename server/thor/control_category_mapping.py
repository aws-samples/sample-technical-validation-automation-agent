#!/usr/bin/env python3
"""
Control Category Mapping for GenAI Competency

Based on GenAICompetencyHelper.java logic - maps controls to designation categories.
Implements Phase 1: Category-level filtering (subcategories added in Phase 2).
"""

# Category-specific controls (DC_CONTROL_LIST from Java)
# These are the ONLY controls that are filtered by designation category
# All other controls are "common" and apply to all categories
DC_CONTROLS = [
    # Generative AI
    "GAIAPP-001", "GAIAPP-002",
    "GAICRE-001", "GAICRE-002", "GAICRE-003", "GAICRE-004", "GAICRE-005",
    "GAIFMS-001", "GAIFMS-002", "GAIFMS-003", "GAIFMS-004",
    "GAIFMS-005", "GAIFMS-006", "GAIFMS-007", "GAIFMS-008",
    "GAIFMO-001",
    "GAIDEV-001", "GAIDEV-002",
    "GAIPBHW-001", "GAIPBHW-002",
    "GAIDSI-001",
    "GAISDG-001",
    # Agentic AI
    "QCHK-001", "QCHK-002", "QCHK-003", "QCHK-004", "QCHK-005", "QCHK-006",
    "QCHKA-001", "QCHKA-002", "QCHKA-003", "QCHKA-004", "QCHKA-005", "QCHKA-006",
    "QCHKT-001", "QCHKT-002", "QCHKT-003", "QCHKT-004", "QCHKT-005", "QCHKT-006", "QCHKT-007",
    "AGAIPS-001", "AGAIPS-002", "AGAIPS-003", "AGAIPS-004",
    # Consulting
    "GENAICEX-001",
    "GENAIPR-001", "GENAIPR-002", "GENAIPR-003", "GENAIPR-004", "GENAIPR-005", "GENAIPR-006"
]

# Category → Control mapping
CATEGORY_CONTROLS = {
    "Generative AI Applications": [
        "GAIAPP-001", "GAIAPP-002",
        "GAICRE-001", "GAICRE-002", "GAICRE-003", "GAICRE-004", "GAICRE-005"
    ],
    
    "Foundation Models and App Development": [
        # FM Training (8 controls)
        "GAIFMS-001", "GAIFMS-002", "GAIFMS-003", "GAIFMS-004",
        "GAIFMS-005", "GAIFMS-006", "GAIFMS-007", "GAIFMS-008",
        # FM Operations
        "GAIFMO-001",
        # App Development
        "GAIDEV-001", "GAIDEV-002",
        # Core (always apply)
        "GAICRE-001", "GAICRE-002", "GAICRE-003", "GAICRE-004", "GAICRE-005"
    ],
    
    "Infrastructure and Data": [
        # Hardware
        "GAIPBHW-001", "GAIPBHW-002",
        # Vector Storage
        "GAIDSI-001",
        # Synthetic Data
        "GAISDG-001",
        # Core (always apply)
        "GAICRE-001", "GAICRE-002", "GAICRE-003", "GAICRE-004", "GAICRE-005"
    ],
    
    "Agentic AI Tools": [
        "QCHKT-001", "QCHKT-002", "QCHKT-003", "QCHKT-004",
        "QCHKT-005", "QCHKT-006", "QCHKT-007"
    ],
    
    "Agentic AI Applications": [
        "QCHKA-001", "QCHKA-002", "QCHKA-003",
        "QCHKA-004", "QCHKA-005", "QCHKA-006"
    ],
    
    "Agentic AI Consulting Services": [
        "QCHK-001", "QCHK-002", "QCHK-003", "QCHK-004", "QCHK-005", "QCHK-006",
        "AGAIPS-001", "AGAIPS-002", "AGAIPS-003", "AGAIPS-004"
    ],
    
    "Generative AI Consulting Services": [
        "GENAICEX-001",
        "GENAIPR-001", "GENAIPR-002", "GENAIPR-003",
        "GENAIPR-004", "GENAIPR-005", "GENAIPR-006"
    ]
}


def get_applicable_controls(designation_category, all_controls):
    """
    Filter controls based on designation category.
    
    Only filters category-specific controls (DC_CONTROLS).
    Common controls (DOC-*, OPE-*, etc.) always apply to all categories.
    
    Args:
        designation_category: String like "Generative AI Applications"
        all_controls: List of all control IDs from CSV
        
    Returns:
        tuple: (applicable_controls, not_applicable_controls)
    """
    if not designation_category:
        # No category info - validate all (conservative)
        return all_controls, []
    
    # Get applicable control IDs for this category
    category_controls = CATEGORY_CONTROLS.get(designation_category, [])
    
    if not category_controls:
        # Unknown category - validate all (conservative)
        return all_controls, []
    
    # Filter only category-specific controls
    applicable = []
    not_applicable = []
    
    for control in all_controls:
        # Remove suffix to get base control
        base_control = control.replace('-SOFTWARE', '').replace('-SERVICE', '')
        
        # Check if this is a category-specific control
        if base_control in DC_CONTROLS:
            # Category-specific - check if in selected category
            if base_control in category_controls:
                applicable.append(control)
            else:
                not_applicable.append(control)
        else:
            # Common control - always applicable
            applicable.append(control)
    
    return applicable, not_applicable


def extract_designation_category_from_ticket(ticket_data):
    """
    Extract designation category from ticket data.
    
    Looks in description field for "Designation categories - [Category Name]"
    
    Args:
        ticket_data: Dict from Tickety API
        
    Returns:
        str: Category name like "Generative AI Applications" or None
    """
    if not ticket_data:
        return None
    
    # PRIMARY: Parse from description field (where it actually is!)
    description = ticket_data.get('description', '')
    if description:
        # Format: "2. Designation categories - Generative AI Applications"
        import re
        match = re.search(r'Designation categories\s*-\s*([^\n]+)', description, re.IGNORECASE)
        if match:
            category = match.group(1).strip()
            # Validate it's a known category
            if category in CATEGORY_CONTROLS:
                return category
    
    # Fallback: Try custom fields
    custom_fields = ticket_data.get('customFields')
    if custom_fields:
        for field_name in ['designationCategories', 'designation_categories', 'DesignationCategories']:
            if field_name in custom_fields:
                category = custom_fields[field_name]
                if isinstance(category, list) and category:
                    return category[0]
                elif isinstance(category, str):
                    return category
    
    # Last resort: Parse from comments
    comments = ticket_data.get('comments', [])
    if comments:
        for comment in comments:
            message = comment.get('message', '')
            if not message:
                continue
            
            for cat in CATEGORY_CONTROLS.keys():
                if cat in message:
                    return cat
    
    return None


# For debugging/verification
if __name__ == "__main__":
    # Test with Ajax case
    test_controls = [
        "GAIAPP-001-SOFTWARE", "GAIAPP-002-SOFTWARE",
        "GAICRE-001-SOFTWARE", "GAICRE-002-SOFTWARE",
        "GAIFMS-001-SOFTWARE", "GAIFMS-002-SOFTWARE",  # Should be N/A
        "GAIDEV-001-SOFTWARE", "GAIDEV-002-SOFTWARE"   # Should be N/A
    ]
    
    applicable, na = get_applicable_controls("Generative AI Applications", test_controls)
    print(f"Applicable: {applicable}")
    print(f"Not Applicable: {na}")
