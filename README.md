== Squid Url Rewriter

This program is for `url_rewrite_program`, and concurrency support.

Require: squid-3.4+

squid config:

```
url_rewrite_program /path/to/squid-urlrewrite
url_rewrite_children 20 startup=1 idle=1 concurrency=10000
```

