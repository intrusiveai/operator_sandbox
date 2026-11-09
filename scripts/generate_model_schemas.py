"""Generate the provider-independent model relay and accounting schemas."""
import argparse
import json
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]/'schemas'
PREFIX = 'urn:operator:schema:'
WIRE = PREFIX+'wire-common:v1alpha1#/$defs/'
MAX = 2**53-1

def obj(properties):
    return dict(type='object', additionalProperties=False, properties=properties, required=list(properties))
def integer(minimum=0):
    return dict(type='integer', minimum=minimum, maximum=MAX)
def documents():
    result = {
        'engine-model-generate-request': obj(dict(
            purpose={'enum':['generation','compaction']},
            conversation={'type':'array','description':'Opaque provider-native conversation history. Operator supplies all request configuration; entries are never merged into top-level options.'})),
        'engine-model-generate-result': obj(dict(
            receipt_id={'$ref':WIRE+'id'},
            response={'description':'Opaque provider response JSON. Provider metadata is not constrained by this interface. A response does not authorize a tool or target action.'},
            context_budget={'$ref':PREFIX+'model-context-budget:v1alpha1'})),
        'model-context-limits': obj({key:integer(1) for key in ('maximum_prompt_tokens','maximum_completion_tokens','warning_threshold_tokens')}),
        'model-context-budget': obj(dict(**{key:integer() for key in ('input_tokens','output_tokens','total_tokens',
            'requested_completion_tokens','reserved_tokens','remaining_prompt_tokens','remaining_model_tokens','remaining_model_turns')},near_prompt_limit={'type':'boolean'})),
    }
    return {stem:dict({'$schema':'https://json-schema.org/draft/2020-12/schema','$id':PREFIX+stem+':v1alpha1'},**doc) for stem,doc in result.items()}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check',action='store_true')
    args = parser.parse_args()
    catalog = json.loads((ROOT/'catalog.json').read_text())
    for stem, document in documents().items():
        filename = stem+'.schema.json'
        rendered = json.dumps(document,indent=2)+'\n'
        path = ROOT/filename
        if args.check:
            if not path.exists() or path.read_text()!=rendered or catalog.get(document['$id'])!=filename:
                raise SystemExit('Stale model schema: '+filename)
        else:
            path.write_text(rendered)
            catalog[document['$id']] = filename
    if not args.check:
        (ROOT/'catalog.json').write_text(json.dumps(dict(sorted(catalog.items())),indent=2)+'\n')


if __name__=='__main__': main()
