#!/usr/bin/env python

import time
import sys
import resource

soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
print(soft)
sys.stdout.flush()
time.sleep(100000)
