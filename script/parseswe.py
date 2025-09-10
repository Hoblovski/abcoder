#!/usr/bin/env python3
import argparse
import os
import subprocess
import glob
from dzyswe import swelite, sweverif, sweall, reponame, repopath

SCRIPT_TEMPLATE = """#!/bin/bash
set -e
# jedi caching
export XDG_CACHE_HOME={outdir}/{instance_id}
rm -rf {outdir}/{instance_id}/repo
git clone {repo_path} {outdir}/{instance_id}/repo
cd {outdir}/{instance_id}/repo
git checkout {commit}
cd -
echo Logs for {instance_id} is at {outdir}/{instance_id}/log.txt
( {command} ) > {outdir}/{instance_id}/log.txt 2>&1
"""

INCLUDE = {
    "flask": "src",
    "matplotlib": "lib/matplotlib",
    "pytest": "src",
    "astropy": "astropy",
    "scikit-learn": "sklearn",
    "seaborn": "seaborn",
    "sympy": "sympy",
    "django": "django",
    "pylint": "pylint",
    "requests": "src",
    "sphinx": "sphinx",
    "xarray": "xarray",
}

DEFAULT_COMMAND_TEMPLATES = {
    "pylsp": "./abcoder parse python {repo_path} -verbose -o {outdir}/{instance_id}.json -include {include_path} -lsp-cache-path {outdir}/{instance_id}/lsp_cache.json",
    "jedi": "./abcoder parse python {repo_path} -verbose -o {outdir}/{instance_id}.json -include {include_path} -lsp-cache-path {outdir}/{instance_id}/lsp_cache.json -lsp jedi-language-server -lsp-flags '--log-file {outdir}/{instance_id}/lsp.log -v'",
}


def compute_jobs(args):
    jobs = []
    for instance_id in args.instance_ids:
        info = sweall[instance_id]
        base_commit = info["base_commit"]
        repo_path = repopath(instance_id)
        jobs.append(
            {"instance_id": instance_id, "repo_path": repo_path, "commit": base_commit}
        )
    return jobs


def create_scripts(args, jobs):
    scripts = []
    for job in jobs:
        instance_id = job["instance_id"]
        repo_path = job["repo_path"]
        commit = job["commit"]
        instance_dir = os.path.join(args.outdir, instance_id)
        include_path = INCLUDE[reponame(instance_id)]
        os.makedirs(instance_dir, exist_ok=True)
        script_path = os.path.join(instance_dir, "main.sh")
        format_dict = {
            "repo_path": repo_path,
            "commit": commit,
            "include_path": include_path,
            "instance_id": instance_id,
            "outdir": args.outdir,
        }
        command = DEFAULT_COMMAND_TEMPLATES.get(args.command, args.command)
        format_dict["command"] = command.format(**format_dict)
        with open(script_path, "w") as f:
            f.write(SCRIPT_TEMPLATE.format(**format_dict))
        os.chmod(script_path, 0o755)
        scripts.append(script_path)
    return scripts


def create_top_script(args, scripts):
    with open(f"{args.outdir}/run_all.sh", "w") as f:
        f.write("#!/bin/bash\nset -e\n")
        if args.parallel:
            f.write("echo Executing in parallel mode...\n")
            f.write("parallel ::: \\\n")
            for script in scripts[:-1]:
                f.write(f"./{script} \\\n")
            f.write(f"./{scripts[-1]}\n")
        else:
            for script in scripts:
                f.write(f"./{script}\n")
    os.chmod(f"{args.outdir}/run_all.sh", 0o755)
    print(f"To run all jobs, execute: ./{args.outdir}/run_all.sh")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "-i",
        "--instance_ids",
        nargs="+",
        required=True,
        help="The list of instance_ids to run",
    )
    parser.add_argument(
        "-o", "--outdir", default="sweout", help="Where to put the output files"
    )
    parser.add_argument(
        "-c",
        "--command",
        default="jedi",
        help="The main command template (can be a template or a name)",
    )
    parser.add_argument(
        "-p", "--parallel", action="store_true", help="Whether to run in parallel"
    )
    args = parser.parse_args()

    jobs = compute_jobs(args)
    scripts = create_scripts(args, jobs)
    create_top_script(args, scripts)


if __name__ == "__main__":
    main()
