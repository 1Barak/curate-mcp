# Directivas de Comportamiento Agéntico: curate-mcp

## 🎯 Propósito
`curate-mcp` proporciona curaduría estructural en memoria RAM para agentes LLM de codificación. En lugar de realizar búsquedas vectoriales ciegas a la topología del software, el servidor mantiene un grafo dirigido de dependencias AST y expone la herramienta `curate_slice`.

## 🛠️ Herramienta: `curate_slice`

### Firma
```json
{
  "name": "curate_slice",
  "arguments": {
    "intent": "descripción de la tarea, símbolo, módulo o flujo a consultar",
    "max_files": 10
  }
}
```

### Cuándo Invocar
1. **Inicio de Tarea / Exploración Topológica:** Antes de modificar un componente, invoca `curate_slice` para identificar los archivos primarios y los controladores/rutas aguas arriba (*entrypoints*).
2. **Localización de Entrypoints:** Para rastrear quién expone o invoca un servicio (`routes`, `controllers`, `handlers`, `api`, `cli`, `main`).
3. **Preservación Estricta de Context Window:** La respuesta devuelta respeta un límite elástico garantizado ($\le 1,000\text{ tokens}$), eliminando el desbordamiento de contexto.
