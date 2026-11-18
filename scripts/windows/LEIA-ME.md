# Caffè — kit de teste no Windows

Este kit foi compilado no Linux. **Não precisa de Go no Windows.** Cada etapa
diz o que mandar de volta.

Pré-requisitos:

- **Git for Windows** com `git` no `PATH` (os testes criam repositórios).
- Para as etapas 3 e 4: **Herdr para Windows** com o servidor rodando, e pelo
  menos um agente (por exemplo Claude Code) instalado **e logado**.
- Coloque a pasta do kit num caminho **sem espaços** (por exemplo `C:\caffe-kit`).

---

## 1. Testes automáticos (W-01, W-02, W-03, F-10)

No PowerShell, dentro da pasta do kit:

```powershell
powershell -ExecutionPolicy Bypass -File .\run-tests.ps1
```

**Mande de volta:** o arquivo `results.txt`. Se algum pacote falhar, mande
também `tests\<pacote>\output.txt`.

O que isso valida no Windows de verdade: árvore de processos morta com job
object (teste `TestValidationCommandLeavesNoProcessesBehind` em `core`),
adaptador Herdr, orquestrador completo com runtime falso, CLI e os arquivos de
referência.

## 2. Spike da interface (F-17)

Rode `tui-spike.exe` em **dois** terminais:

- no **Windows Terminal**;
- no **console clássico**: `Win+R` → `conhost.exe C:\caffe-kit\tui-spike.exe`.

Em cada um:

1. Redimensione a janela: o tamanho deve mudar na tela.
2. Veja se `ticks` continua contando sozinho.
3. Aperte setas, `ctrl+x`, `alt+x`, `F1`, `shift+tab`: o `last key` deve mostrar cada um.
4. `m` liga o mouse: clique e role a roda.
5. `e` abre o Notepad: escreva algo, salve, feche → deve voltar para o spike.
6. `s` abre um `cmd`: digite `exit` → deve voltar para o spike.
7. `q` sai. Copie o resumo que aparece.
8. Rode de novo e aperte `p` (pânico de propósito): o terminal precisa voltar
   usável (cursor, eco do teclado, cores).

**Mande de volta:** os dois resumos e o que você observou nos itens 1, 4 e 8.

> No Linux, rode `dist/linux/tui-spike` e faça o mesmo (com o editor do
> `$EDITOR`, ou `vi`).

## 3. Uma run de verdade pelo Herdr (W-06)

Num repositório de teste (não use um projeto real):

```powershell
mkdir C:\caffe-teste; cd C:\caffe-teste
git init -b main
git commit --allow-empty -m init
C:\caffe-kit\caffe.exe init
C:\caffe-kit\caffe.exe agents
C:\caffe-kit\caffe.exe run --plan C:\caffe-kit\plans\hello.json "Add a hello file"
```

Se um agente perguntar se confia na pasta, a run mostra o comando de *attach*;
rode-o em outra janela, aceite e volte.

**Mande de volta:** a saída de `caffe agents`, a saída da run e
`C:\caffe-kit\caffe.exe inspect <id>`.

## 4. Run destacada sobrevive ao console (W-04)

Ainda em `C:\caffe-teste`, edite `.caffe\config.yaml` e troque
`commands: []` (em `validation:`) por:

```yaml
    commands:
        - ping -n 40 127.0.0.1
```

Depois, num **cmd** novo:

```bat
C:\caffe-kit\caffe.exe -C C:\caffe-teste run --detach --plan C:\caffe-kit\plans\slow.json "detach test"
```

1. Assim que aparecer `started in the background`, **feche a janela** (no X).
2. Abra outro cmd e rode `C:\caffe-kit\caffe.exe -C C:\caffe-teste status`:
   a run deve estar `executing`/`validating`.
3. Depois de ~40 s, `caffe runs` deve mostrar `completed`.
4. Repita, mas desta vez aperte **Ctrl-C** na janela logo depois de iniciar:
   a run também deve continuar.

**Mande de volta:** o que `status` e `runs` mostraram nos dois casos, e o
`.caffe\artifacts\<id>\console.log` se algo falhar.
