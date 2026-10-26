package control

// BoundResult validates an owner's final result before publication or receipt
// storage. Invalid presentation data never erases evidence of an admitted effect.
// The request must already have passed admission validation.
func BoundResult(request Request, result Result, mutation bool) Result {
	fault := result.Validate()
	if result.RequestID != request.ID {
		fault = invalid("request_id", "result does not belong to the admitted request")
	}
	if fault == nil {
		return result
	}
	out := Result{RequestID: request.ID, OperationID: request.ID, State: Failed,
		Fault: &Fault{Code: OperationFailed, Field: fault.Field, Message: "Control result rejected: " + fault.Message + "; inspect the original operation receipt and use a smaller bounded read where available. Do not repeat a mutation to recover output."}}
	if id("operation_id", result.OperationID, true) == nil {
		out.OperationID = result.OperationID
	}
	if mutation && result.State != Failed {
		out.State = Partial
		if result.State == Uncertain {
			out.State = Uncertain
		}
	}
	for _, resource := range result.ResourceIDs {
		if len(out.ResourceIDs) == MaxPageLimit {
			break
		}
		if id("resource_id", resource, true) == nil {
			out.ResourceIDs = append(out.ResourceIDs, resource)
		}
	}
	out.Revision = result.Revision
	if jsonValue("output", result.Output, MaxValueBytes, true) == nil {
		out.Output = result.Output
	}
	return out
}
