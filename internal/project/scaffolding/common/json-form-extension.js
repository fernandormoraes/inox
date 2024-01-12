//This extension encodes form parameters to JSON, array elements are merged and values are conveted to the correct type.
//For example the value of an <input type=number> is converted to a number.
htmx.defineExtension('json-form', {
    onEvent: function (name, evt) { 
        if (name === "htmx:configRequest") { 
            evt.detail.headers['Content-Type'] = "application/json"
        }
    },
    encodeParameters: function(xhr, params, form) {
        xhr.overrideMimeType('text/json');
        return JSON.stringify(transformFormParams(params, form));
    }
});

/**
 * transformFormParams transforms form parameters into a correctly typed structure:
 * - Entries whose key contains a property name (e.g `user.name`) or an array index (e.g. `elements[0]`)
 *   are converted into nested structures (objects and arrays).
 * - Values from number and range inputs are converted to numbers.
 * - Values from 'boolean checkboxes' are converted to boolean.
 *   A boolean checkbox is defined as a pair of two <input type=checkbox> elements 
 *   with values in the list ['yes, 'no', 'true', 'false']
 * - Non-array values from other checkboxes are put in an array if necessary.
 * @param {Record<string, unknown>} params 
 * @param {HTMLFormElement} form
 * @param {boolean} changeTypes
 */
function transformFormParams(params, form){
    /** @type {Record<string,unknown>} */
    const transformed = {}

    transform_loop:
    for(let [key, value] of Object.entries(params)){

        value = convertFormParam(key, value, form)

        const logSegmentError = () => console.error(`invalid segment in form key \`${key}\``)

        if(key[key.length-1] == ']' || key.includes('.')){ //nested structure path
            /** @type {(string|number)[]} */
            const path = []
            let searchOpeningBracket = key[key.length-1] == ']'
            const iterationStart = searchOpeningBracket ? key.length-2 : key.length-1
            let segmentEnd = iterationStart + 1

            for (let i = iterationStart; i >= 0; i--) {
                switch(key[i]){
                case '[':
                    if(searchOpeningBracket && i > 0){
                        searchOpeningBracket = false
                        const index = Number.parseInt(key.slice(i+1, segmentEnd))
                        if(isNaN(index)){
                            logSegmentError()
                            continue transform_loop
                        }
                        path.unshift(index)
                        segmentEnd = i
                    } else {
                        logSegmentError()
                        continue transform_loop
                    }
                    break
                case ']':
                    if(searchOpeningBracket || i == 0){
                        logSegmentError()
                        continue transform_loop
                    }
                    searchOpeningBracket = true
                    segmentEnd = i
                    break
                case '.':
                    if(searchOpeningBracket || i == 0){
                        logSegmentError()
                        continue transform_loop
                    }
                    const propertyName = key.slice(i+1,segmentEnd)
                    path.unshift(propertyName)
                    segmentEnd = i
                    break
                }
            }

            if(searchOpeningBracket){
                logSegmentError()
                continue transform_loop
            }

            path.unshift(key.slice(0, segmentEnd))


            if(path.length == 1){
                transformed[key] = value
                continue transform_loop
            }

            /** @type {any} */
            let current = transformed

            //create nested structure

            for (let i = 0; i < path.length; i++) {
                const segment = path[i]
                if(! (segment in current)){
                    if(i == path.length-1) { //last segment
                        current[segment] = value
                        break
                    }

                    if(typeof path[i+1] == 'number'){
                        current[segment] = []
                    } else {
                        current[segment] = {}
                    }
                }
                current = current[segment]
            }
        } else {
            transformed[key] = value
        }
    }

    return transformed
}

/**
 * convertFormParam changes the type of a value based on the type and number of 
 * input elements with a name equal to $key.
 * @param {string} key 
 * @param {unknown} value 
 * @param {HTMLFormElement} form 
 */
function convertFormParam(key, value, form){
    const inputs = Array.from(form.querySelectorAll(`input[name="${key}"]`))
    if(inputs.length == 0 || Array.isArray(value)) {
        return value
    }

    const firstFoundInput = /** @type {HTMLInputElement} */ (inputs[0])
    const booleanCheckboxValues = ['yes', 'no', 'true', 'false']

    switch(inputs.length){
    case 1:
        //convert value to number if necessary.
        switch(firstFoundInput.type){
        case 'number': case 'range':
            return Number(value)
        default:
            return value
        }
    case 2:
        switch(firstFoundInput.type){
        case 'checkbox':
            if(inputs.every(input => booleanCheckboxValues.includes(input.value.toLowerCase())))
                return convertBooleanCheckboxValue(value)
            }
            return [value]            
        default:
            return value
    case 3:
        switch(firstFoundInput.type){
        case 'checkbox':
            return [value]            
        default:
            return value
        }
    }
}

function convertBooleanCheckboxValue(value){
    switch(String(value).toLowerCase()){
    case 'yes': case 'true':
        return true
    default:
        return false
    }
}