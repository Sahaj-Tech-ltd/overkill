---
name: docx
description: Use when creating, editing, or reading Word (.docx) documents — reports, letters, invoices, forms, or any document the user wants as a downloadable .docx file. Also use when converting markdown to docx.
---

# DOCX — Word Document Creation

## Overview

Create and edit `.docx` files using `python-docx`. Self-contained — no cloud services, no templates needed.

## Quick Start

```bash
pip install python-docx
```

## Create a Document

```python
from docx import Document
from docx.shared import Inches, Pt, RGBColor
from docx.enum.text import WD_ALIGN_PARAGRAPH

doc = Document()

# Title
title = doc.add_heading('Report Title', level=0)
title.alignment = WD_ALIGN_PARAGRAPH.CENTER

# Paragraph with formatting
p = doc.add_paragraph('Body text here.')
run = p.add_run(' Bold text.')
run.bold = True

# Bullet list
doc.add_paragraph('Item 1', style='List Bullet')
doc.add_paragraph('Item 2', style='List Bullet')

# Table
table = doc.add_table(rows=3, cols=2, style='Light Shading Accent 1')
table.cell(0, 0).text = 'Header 1'
table.cell(0, 1).text = 'Header 2'

doc.save('/tmp/output.docx')
```

## Common Patterns

### Markdown → DOCX

For simple conversion, write markdown to a temp file, convert with pandoc:
```bash
pandoc input.md -o output.docx
```

For programmatic control, parse markdown and build the docx manually via python-docx.

### Headers & Footers
```python
section = doc.sections[0]
header = section.header
header.paragraphs[0].text = "CONFIDENTIAL"
```

### Images
```python
doc.add_picture('/path/to/image.png', width=Inches(4.0))
```

### Page breaks
```python
doc.add_page_break()
```

## Output

Always save to a user-accessible path. Inform the user of the file location so they can download/open it.

## Pitfalls

- python-docx doesn't convert markdown natively — use pandoc for quick conversion
- Table styles vary by system — test with `'Light Shading Accent 1'` as safe default
- Large images bloat docx — resize before inserting
