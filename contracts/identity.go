package contracts

import "bytes"

// RegistryDigests identifies the exact parsed catalog mapping and operation
// registry loaded into this Protocol. The catalog digest does not bind schema
// file bytes; the forthcoming package inventory supplies that separate binding.
func (p *Protocol) RegistryDigests() map[string]string {
	return map[string]string{"catalog_digest": p.catalog.digest, "operations_digest": p.operationsDigest}
}

// ValidateLaunchIdentities adds canonical identity verification to launch-content
// validation. Package/release authenticity, native-source identity and verification
// of actual reference/skill bytes remain independent caller responsibilities.
func (p *Protocol) ValidateLaunchIdentities(messages [][]byte, tree, set []byte, skills [][]byte, context, bundle, prompt []byte) error {
	if err := p.ValidateLaunchContent(messages, tree, set, skills, context, bundle, prompt); err != nil {
		return err
	}
	init, _ := p.ValidateControl("host", messages[2])
	body := init["body"].(map[string]any)
	for key, raw := range map[string][]byte{"input_tree": tree, "skill_set": set} {
		digest, err := CanonicalDigest(raw, InputTreeManifestLimit)
		if err != nil {
			return err
		}
		if body[key].(map[string]any)["object_digest"] != digest {
			return ErrProtocol
		}
	}
	digest, err := CanonicalDigest(context, EngineContextLimit)
	if err != nil {
		return err
	}
	if body["engine_context_object_digest"] != digest {
		return ErrProtocol
	}
	skillSet, _ := p.ValidateSkillSet(set)
	loading := map[string]any{}
	for key, value := range skillSet {
		if key != "loading_digest" {
			loading[key] = value
		}
	}
	digest, err = objectDigest(loading, ControlLimit)
	if err != nil {
		return err
	}
	if skillSet["loading_digest"] != digest {
		return ErrProtocol
	}
	for i, raw := range skills {
		digest, err := CanonicalDigest(raw, SkillManifestLimit)
		if err != nil {
			return err
		}
		descriptor := skillSet["skills"].([]any)[i].(map[string]any)["manifest"].(map[string]any)
		if descriptor["object_digest"] != digest {
			return ErrProtocol
		}
	}
	c, _ := p.ValidateEngineContext(context)
	target := c["target"].(map[string]any)
	digest, err = objectDigest(target["capabilities"], EngineContextLimit)
	if err != nil {
		return err
	}
	if target["capability_projection_digest"] != digest {
		return ErrProtocol
	}
	contract := c["contract"].(map[string]any)
	if pin, verified := p.PackageIdentity(); verified && (contract["version"] != pin.Version || contract["digest"] != pin.Digest) {
		return ErrProtocol
	}
	for key, digest := range p.RegistryDigests() {
		if contract[key] != digest {
			return ErrProtocol
		}
	}
	return nil
}

// ValidateArtifactContent checks bytes against a validated artifact-begin request
// resolved from trusted upload state. It neither commits the object nor establishes
// campaign membership. jcs-v1 asserts already-canonical bytes; it never rewrites them.
func (p *Protocol) ValidateArtifactContent(beginRequest, content []byte) error {
	request, err := p.ValidateRequest(beginRequest)
	if err != nil {
		return err
	}
	if request["operation"] != "engine.artifact_begin" {
		return ErrProtocol
	}
	descriptor := request["body"].(map[string]any)["artifact"].(map[string]any)
	if number(descriptor["size_bytes"]) != int64(len(content)) || descriptor["digest"] != RawDigest(content) {
		return ErrProtocol
	}
	if descriptor["canonicalization"] == "jcs-v1" {
		canonical, err := Canonicalize(content, 16<<20)
		if err != nil {
			return err
		}
		if !bytes.Equal(content, canonical) {
			return ErrProtocol
		}
	}
	return nil
}
