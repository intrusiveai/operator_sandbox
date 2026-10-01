"""Safety disposition for the closed Azure response annotation subset."""


def filtered(response):
    for key in ('prompt_filter_results', 'content_filters', 'choices'):
        for annotation in response.get(key, []):
            if annotation.get('blocked') is True:
                return True
            if any(result['filtered'] for result in annotation.get('content_filter_results', {}).values()):
                return True
    return False
