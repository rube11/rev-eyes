"""Bounded, no-key probes against a local SearXNG service (not production).

Run with evaluation-settings.yml; never retries a blocked/CAPTCHA engine.
The optional report is exclusive-create so previous results are preserved.
"""

import argparse
import concurrent.futures
import datetime
import json
import time
import urllib.parse
import urllib.request


def probe(base_url, engine, query, category, recency=""):
    # Sending both engines and categories unions the selections in SearXNG.
    # Omit categories so one probe really queries only the named engine.
    form = {"q": query, "engines": engine,
            "format": "json", "language": "en", "safesearch": "2"}
    if recency:
        form["time_range"] = recency
    started = time.monotonic()
    report = {"engine": engine, "query": query, "category": category}
    try:
        request = urllib.request.Request(base_url.rstrip("/") + "/search",
                                         data=urllib.parse.urlencode(form).encode())
        with urllib.request.urlopen(request, timeout=8) as response:
            payload = json.load(response)
        report["unresponsive_engines"] = payload.get("unresponsive_engines", [])
        report["result_count"] = len(payload.get("results", []))
        report["results"] = [{key: item.get(key) for key in
                              ("title", "url", "content", "engines", "publishedDate")}
                             for item in payload.get("results", [])[:8]]
        reported = {name for item in payload.get("results", []) for name in item.get("engines", [])}
        if reported - {engine}:
            report["error"] = "engine isolation failed: " + ", ".join(sorted(reported))
    except Exception as error:
        report["error"] = str(error)
    report["elapsed_ms"] = round((time.monotonic() - started) * 1000)
    return report


def run_engine(base_url, engine):
    category = "news" if engine.endswith("news") else "general"
    queries = [("Las Vegas Raiders roster transactions", "week")] if category == "news" else [
        ("Las Vegas pho", ""),
        ("Red Rock Canyon Nevada scenic drive reservation site:blm.gov", ""),
        ("Seattle ramen restaurants", ""),
    ]
    reports = []
    for query, recency in queries:
        result = probe(base_url, engine, query, category, recency)
        reports.append(result)
        failures = json.dumps(result.get("unresponsive_engines", [])).lower()
        if any(marker in failures for marker in ("captcha", "429", "access denied", "403")):
            break
    return reports


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8888")
    parser.add_argument("--engines", nargs="+", default=["google", "bing", "yahoo", "mojeek", "google news", "bing news"])
    parser.add_argument("--output")
    args = parser.parse_args()
    target = urllib.parse.urlparse(args.base_url)
    if target.scheme != "http" or target.hostname not in ("127.0.0.1", "localhost", "::1"):
        parser.error("probes must target a localhost HTTP service")
    with urllib.request.urlopen(args.base_url.rstrip("/") + "/config", timeout=8) as response:
        available = {item["name"] for item in json.load(response)["engines"]}
    if set(args.engines) - available:
        parser.error("unknown engine: " + ", ".join(sorted(set(args.engines) - available)))
    report = {"started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "probes": []}
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(run_engine, args.base_url, engine) for engine in args.engines]
        for future in concurrent.futures.as_completed(futures):
            for item in future.result():
                report["probes"].append(item)
                print(json.dumps(item), flush=True)
    if args.output:
        with open(args.output, "x", encoding="utf-8") as output:
            json.dump(report, output, indent=2)


if __name__ == "__main__":
    main()
