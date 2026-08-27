#!/usr/bin/env python3
import pathlib, re, sys
root=pathlib.Path(__file__).resolve().parents[1]
patterns=[r'X-Plex-Token\s*[:=]\s*[A-Za-z0-9_-]{10,}',r'(?i)\b(?:token|api[_-]?key|password)\s*[:=]\s*["\'][^"\']+["\']',r'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----']
found=[]
for p in root.rglob('*'):
    if not p.is_file() or '.git' in p.parts or p.name in {'check-secrets.py'} or p == root/'api/plex-pms.openapi.json': continue
    try: text=p.read_text(errors='ignore')
    except OSError: continue
    for pat in patterns:
        if re.search(pat,text): found.append(str(p.relative_to(root))+': '+pat)
if found:
    print('\n'.join(found)); sys.exit(1)
print('secret scan: clean')
