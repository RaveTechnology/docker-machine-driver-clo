import hashlib,os,pathlib,re,sys,yaml
version=sys.argv[1].removeprefix('v')
if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?',version):raise ValueError('invalid release version')
if version!=pathlib.Path('VERSION').read_text().strip():raise ValueError('tag and source version mismatch')
p=pathlib.Path('dist/clo-linux-amd64.tar.gz');data=yaml.safe_load(pathlib.Path('deploy/node-driver.yaml').read_text())
data['spec']['url']='https://github.com/'+os.environ.get('GITHUB_REPOSITORY','RaveTechnology/docker-machine-driver-clo')+'/releases/download/v'+version+'/'+p.name
data['spec']['checksum']=hashlib.sha256(p.read_bytes()).hexdigest()
pathlib.Path('dist/node-driver.yaml').write_text(yaml.safe_dump(data,sort_keys=False))
