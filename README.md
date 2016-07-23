== Squid Url Rewriter

This program is for `url_rewrite_program`, and concurrency support.


Usage:

```
url_rewrite_program /path/to/squid-urlrewrite
url_rewrite_children 2 startup=2 idle=2 concurrency=10000
```

