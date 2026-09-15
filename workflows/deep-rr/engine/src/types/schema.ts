// Schema — a single StructuredOutput JSON-schema node. Every agent's output contract and every shared
// schema brick (RABBITHOLE, PAGE, …) is one of these literals. Recursive: `properties` maps field name →
// nested Schema, `items` is the element Schema of an array. All but `type` are optional so a leaf like
// `{ type: 'string' }` and a deep object both satisfy it.
// `type` may be a UNION array (e.g. ['string','null']): an OPTIONAL field a worker model routinely fills
// with null must validate, not burn a schema-mismatch retry — the engine null-scrubs at ingest.
type SchemaType = 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null';
export interface Schema {
  type: SchemaType | SchemaType[];
  properties?: Record<string, Schema>;
  items?: Schema;
  required?: string[];
  description?: string;
  enum?: string[];
  maxItems?: number;
  additionalProperties?: Schema; // an OPEN-KEYED object (e.g. Record<string, number>) — the schema every dynamic key must satisfy
}
