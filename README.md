# liusha

> **免责声明**：本项目仅用于安全研究与学习，严禁使用本项目从事违反所在地区法律的行为，并对自身使用行为承担全部责任，作者对任何滥用不承担任何责任。

## 部署

### 全容器

```bash
git clone --branch v0.7.0 https://github.com/V3teran/liusha.git && cd liusha  # x-release-please-version
cp .env.example .env   # 填必填项
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

### 升级

代码与镜像同版本：检出哪个发布 tag 就部署哪个版本（版本列表见 [Releases](https://github.com/V3teran/liusha/releases)），数据库迁移随 `up` 自动执行。

```bash
git fetch --tags && git checkout vX.Y.Z   # 改过 config/config.yaml 的先 git stash，切换后 git stash pop
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

回滚同理：检出旧 tag 再 pull / up（数据库迁移只进不退，跨含迁移的版本回滚前先备份）。

## 许可证

Copyright 2026 V3teran

本项目以 [Apache License 2.0](LICENSE) 授权：可以自由使用、修改、分发与商用；再分发时须保留许可证与 [NOTICE](NOTICE) 中的署名，并标明所做的修改。v0.6.2 及之前发布的版本仍按当时的 MIT 许可证授权。
