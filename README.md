# CLO Rancher Node Driver

Первый этап интеграции CLO: управление VM через Rancher/machine. Код изолирован
от сервисов rave-technology. [UI Extension](https://github.com/RaveTechnology/rancher-clo-ui) выпускается отдельно.
Production rollout, CCM и LoadBalancer в этот модуль не входят. Статус реальной проверки Node Driver описан в
[acceptance report](docs/acceptance.md).

Зависимости закреплены: CLO SDK v3.2.0 (HTTP API v2), rancher/machine
v0.15.0-rancher145.3 для Rancher 2.15.3, Go 1.27, Linux amd64.

## Сборка и регистрация

```sh
python3 -m venv /tmp/clo-test-venv
/tmp/clo-test-venv/bin/pip install -r scripts/requirements-test.txt
export PATH="/tmp/clo-test-venv/bin:$PATH"
make check
make package
cat dist/SHA256SUMS
```

Опубликуйте неизменяемый `dist/clo-linux-amd64.tar.gz` по адресу, доступному
Rancher и machine-provision pods. Подставьте URL и checksum из `SHA256SUMS`
в [deploy/node-driver.yaml](deploy/node-driver.yaml) и примените к выбранному
management cluster. Манифест намеренно содержит нерабочий example URL.
Регистрация бинарника выполняет код с полномочиями provisioning; используйте
собственный артефакт и его checksum.

Annotations создают `clocredentialConfig.apiToken` в cloud credentials Rancher 2.15.3.
`apiToken` — единственное credential field. `projectId` остаётся параметром
machine config. [Контракт annotations Rancher](https://extensions.rancher.io/extensions/next/provisioning/node-driver/about-drivers).
`userDataFile:userDataFile` передаёт содержимое cloud-init через файл.
Токен приходит в `CLO_API_TOKEN`, не сериализуется в machine config и
не включается в cloud-init. Не передавайте токен в shell arguments.

Параметры `CloConfig`:

| Поле | Назначение |
|---|---|
| `projectId`, `imageId` | Существующие CLO project и Ubuntu 24.04 image |
| `vcpus`, `ramGb`, `bootGb` | Shared CPU, RAM GB, boot GB; defaults 2/4/20 |
| `publicIpId` | Пусто: private-only; иначе существующий reserved IPv4 |
| `sshAddress` | `private` (default) либо `public` |
| `userDataFile` | Cloud-init contents; Rancher сам передаёт registration payload |
| `timeoutSeconds` | Timeout операции, default 900, максимум 3600 |

Фактическая schema Rancher является источником имён и типов полей; проверяйте
её после регистрации. Node Driver не создаёт проекты, NAT и public IP.
Management должен достигать private SSH в том же проекте; NAT/DNS нужны до
первого запуска private VM. CIDR проектов могут совпадать.

Образ CLO задаёт policy routing через `/etc/netplan/netconfig.yaml`. В lab
обнаружены исключения для стандартных RKE2 CIDR10.42/16 и10.43/16. При других
Pod/Service CIDR требуются destination rules в main table с приоритетом раньше
source rules CLO. Для management10.44/16+10.45/16 отсутствие таких правил
вызывало timeout pod→API; правила priority5 восстановили связь. Драйвер пока
не извлекает произвольные CIDR из Rancher cluster config: подготовьте правила
в дополнительном cloud-init до регистрации и проверьте выбранную сеть в lab.

Для Canal обязательно задайте private CNI selection на уровне Rancher cluster:
[deploy/canal-private-values.yaml](deploy/canal-private-values.yaml) — значения
для `spec.rkeConfig.chartValues`. Укажите gateway своей private сети и сохраните
штатные аргументы точной версии chart. `--iface-can-reach` выбирает интерфейс
по маршруту к private gateway на каждой ноде, независимо от имени NIC.
Одного private `node-ip` недостаточно: без настройки Flannel на public VM
выбирает внешний default route. Значения Rancher автоматически получает и
replacement-нода. [Настройки сети RKE2](https://docs.rke2.io/networking/basic_network_options),
[реализация Flannel v0.28.9](https://github.com/flannel-io/flannel/blob/v0.28.9/main.go).
Задайте профиль при создании кластера, до добавления public pool.

## Сохранность и восстановление

`Create` сохраняет уникальное имя попытки до первого POST. `PreCreateCheck`
готовит pending-state и ключ до того, как Rancher сохраняет machine Secret.
Только исходный процесс отправляет первый POST. После восстановления pending
состояния драйвер ищет ровно одну VM с точным именем в сохранённом проекте.
При нуле/нескольких совпадениях он останавливается: требуется сверка оператора,
автоматический новый POST запрещён. Это может потребовать ручной сверки и при
прерывании до отправки POST.

Дополнительный журнал `machines/<name>/clo-state.json` записывается атомарно
с mode 0600. Rancher/machine сохраняет machine directory в Secret. Token в
журнале отсутствует. Не редактируйте ownership IDs для обхода ошибки проверки.

`Remove` сверяет проект, имя VM и владельца сохранённого boot volume, передаёт
только этот volume ID и `delete_addresses: []`. Позже подключённые data volumes
и reserved public IP сохраняются. Если VM отсутствует, но boot volume остался,
очистка не объявляется успешной. Это требует отдельной сверки остатков.

Публичный pool имеет quantity=1 и один заранее выделенный IP. Replacement:
сначала удалить старую machine через Rancher, дождаться свободного IP, затем
создать новую. Одновременный rolling replacement с тем же IP не поддерживается.
Выбор public SSH не меняет private RKE2 `node-ip`.

Сложный cloud-init проверяется до создания: YAML mapping, без aliases,
дубликатов ключей или конфликтов с CLO network script. Ошибка private IP
останавливает registration, не меняя shell options исходных команд.
Composer сохраняет исходные YAML nodes/scalar styles, включая YAML 1.1 значения
cloud-init. Тесты guest script и независимой проверки YAML требуют Python 3
и закреплённый PyYAML из `scripts/requirements-test.txt`.

При запрошенном public IPv4 Create ожидает его присутствия на VM, статуса
ACTIVE и привязки к её ID при любом `sshAddress`; выбор private SSH не ослабляет
эту проверку.

Для native CLO volumes нужны отдельные CSI identity/fencing и recovery tests.
Наличие этого драйвера само по себе не создаёт StorageClass или Service LB.

Проверка management без чтения Secret:

```sh
bash scripts/check-lab.sh YOUR_LAB_CONTEXT clo-driver-lab
```


## Публичный выпуск

Готовые архив, SHA256SUMS и манифест NodeDriver с точной контрольной суммой доступны
в [Releases](https://github.com/RaveTechnology/docker-machine-driver-clo/releases).
Проверьте SHA256SUMS и содержимое `node-driver.yaml`, затем примените манифест
в явно выбранном Rancher management context. Перед добавлением worker подготовьте
приватную сеть/NAT и проверку SSH. Драйвер не является CSI или балансировщиком.

Общий API/bootstrap закреплён как github.com/RaveTechnology/clo-cloud-go v0.1.0.
Лицензия Apache-2.0.
