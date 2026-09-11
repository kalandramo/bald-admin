# go-bald-admin-web

锟斤拷锟斤拷 Vue 3 + TypeScript + Element Plus + Vite 锟斤拷 Bald 锟斤拷芄锟斤拷锟斤拷锟教ㄇ帮拷恕锟?

## 锟斤拷锟斤拷锟斤拷锟斤拷

```bash
pnpm i
pnpm dev
```

前锟斤拷锟斤拷锟斤拷锟斤拷 http://localhost:3333 锟斤拷API 锟斤拷锟斤拷锟斤拷锟斤拷锟?http://localhost:8080 锟斤拷

## 锟斤拷说锟街凤拷锟斤拷锟?

锟斤拷 vite.config.ts 锟斤拷锟睫改达拷锟斤拷目锟疥：

```ts
proxy: {
  "/v1": {
    target: "http://localhost:8080",
    changeOrigin: true
  }
}
```

## 默锟斤拷锟剿猴拷

- 锟矫伙拷锟斤拷: admin 锟斤拷锟斤拷锟斤拷: 12345678 锟斤拷锟斤拷锟节猴拷舜锟斤拷锟斤拷锟?

## 锟斤拷锟斤拷锟藉单

| 模锟斤拷     | 路锟斤拷                 | 说锟斤拷                      |
| ------- | ------------------- | ------------------------ |
| 锟解户锟斤拷锟斤拷 | /tenants            | 锟解户 CRUD锟斤拷锟斤拷 admin 锟缴凤拷锟斤拷 |
| 锟矫伙拷锟斤拷锟斤拷 | /users              | 锟斤拷前锟解户锟矫伙拷 CRUD锟斤拷锟斤拷 admin |
| 锟剿碉拷锟斤拷锟斤拷 | /system/menus       | 锟剿碉拷锟斤拷维锟斤拷锟斤拷锟斤拷 admin       |
| 权锟睫癸拷锟斤拷  | /system/permissions | 权锟睫碉拷 + 锟斤拷色锟斤拷锟皆ｏ拷锟斤拷 admin  |
| 锟街碉拷锟斤拷锟? | /dicts              | 锟街碉拷锟斤拷锟斤拷 + 锟斤拷目锟斤拷锟斤拷 admin  |
| 锟侥硷拷锟斤拷锟斤拷 | /files              | 锟较达拷/锟斤拷锟斤拷/删锟斤拷锟斤拷锟斤拷 admin   |
| 锟斤拷锟斤拷锟街? | /audits             | 锟斤拷锟斤拷/锟斤拷录锟斤拷志锟斤拷询锟斤拷锟斤拷 admin |

## 权锟斤拷说锟斤拷

- 页锟斤拷锟斤拷锟酵拷锟?meta.roles 锟斤拷锟狡ｏ拷锟斤拷前锟斤拷 admin 锟斤拷色锟缴凤拷锟绞癸拷锟斤拷页锟斤拷
- 锟斤拷钮锟斤拷权锟斤拷通锟斤拷 v-permission 指锟斤拷实锟街ｏ拷锟斤拷锟?whoami 锟斤拷未锟斤拷锟斤拷 permissions 锟街段ｏ拷
- 锟斤拷色锟斤拷锟斤拷锟睫改猴拷锟斤拷锟斤拷锟斤拷锟斤拷锟斤拷锟叫?

## 锟斤拷锟斤拷

```bash
pnpm build
pnpm test
pnpm lint
```
