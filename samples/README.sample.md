# mdexec sample

## Setup
```bash mdexec name=setup tags=init
echo "Setting up for {{PROJECT}}"
```

## Lint
```bash mdexec name=lint deps=setup tags=ci
echo "Lint OK"
```

## Build
```bash mdexec name=build deps=setup,lint tags=ci,build timeout=30
echo "Build for ${TARGET:-dev}"
```

## Python demo
```python mdexec name=py-hello
import os
print(f"Hello, {os.getenv('NAME','world')}")
```
