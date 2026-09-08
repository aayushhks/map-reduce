# Report page

`index.html` is the whole site: one self-contained file with the measurement data
embedded as JSON. Every figure on the page is rendered from that data at load, so
the page cannot drift from what is committed in `bench/results/`.

Rebuild the embedded data after new measurement runs:

    python3 site/build.py

Deployed at https://map-reduce-orcin.vercel.app/ — it is plain static files, so
any host works.

    vercel deploy --prod site
    aws s3 sync site s3://BUCKET --delete && aws cloudfront create-invalidation --distribution-id ID --paths '/*'

There is no build step and no runtime dependency beyond Google Fonts, which
falls back to system faces if unreachable.
