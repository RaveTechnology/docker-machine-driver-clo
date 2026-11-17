# Приёмка

Node Driver испытан в отдельном CLO lab с Rancher2.15.3/RKE2 1.36.6.
Проверены private/public provisioning, регистрация Ready, CNI через private NIC,
удаление/замена ноды с сохранением IPv4 и сторонних data volumes, ownership checks
и обработка неоднозначного native create. Подробные regression tests находятся
в driver/ и общей библиотеке clo-cloud-go.

Это не подтверждение готовности CSI/native расширения дисков или миграции rave-technology.
Публичный релиз изменяет упаковку и пути Go-модулей; исходные алгоритмы сохранены.
