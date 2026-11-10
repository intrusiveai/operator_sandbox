import json
from pathlib import Path
import unittest
from jsonschema import Draft202012Validator, ValidationError
from operator_contracts.schema_diagnostic import schema_argument_issue

class UnsupportedFieldsTest(unittest.TestCase):
    def test_shared_matrix(self):
        root=Path(__file__).resolve().parents[3]
        for case in json.loads((root/'schemas/fixtures/unsupported-fields.json').read_text()):
            with self.subTest(case['name']):
                validator=Draft202012Validator(case['schema'])
                with self.assertRaises(ValidationError) as caught:validator.validate(case['bad'])
                issue=schema_argument_issue(caught.exception)
                self.assertEqual(issue.field_path,case['path'])
                self.assertEqual(issue.unsupported_fields['names'],case['names'])
                for part in case['contains']:self.assertIn(part,issue.correction)
                self.assertNotIn('SYNTHETIC_SECRET',issue.correction)
                validator.validate(case['good'])
