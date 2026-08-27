#!/usr/bin/env python3
import json, pathlib
root=pathlib.Path(__file__).resolve().parents[1]
spec=json.loads((root/'api/plex-pms.openapi.json').read_text())
ops=sum(1 for path in spec['paths'].values() for method in path if method in {'get','post','put','patch','delete','head','options'})
if ops < 258: raise SystemExit(f'operation count regressed: {ops}')
print(f'API coverage contract: {ops} documented operations across {len(spec["paths"])} paths')
