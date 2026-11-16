# AsyncAPI document validation schema

`3.0.0.json` is the unmodified AsyncAPI document schema from
https://github.com/asyncapi/spec-json-schemas/blob/v6.9.0/schemas/3.0.0.json
(Apache-2.0; see LICENSE). It validates the document structure offline. Payload
schemas explicitly use JSON Schema 2020-12, which our Go/Python validators compile
and exercise separately. Document validation alone is not implementation
conformance. This development tool is excluded from runtime contract identity.
