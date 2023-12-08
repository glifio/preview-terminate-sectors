import express from "express";

const app = express();

import path from "path";
import fs from "fs";
import ndjson from "ndjson";
import cors from "cors";

app.use(cors());

app.use( express.static( path.join( "public" ) ) );

app.get( "/", ( req, res ) => {
    let readStream = fs.createReadStream( "todos.ndjson" ).pipe( ndjson.parse() );

    const chunks = [];

    readStream.on( "data", ( data ) => {
        chunks.push( JSON.stringify( data ) );
    } );

    readStream.on( "end", () => {
        const id = setInterval( () => {
            if ( chunks.length ) {
                res.write( chunks.shift() + "\n" );
            } else {
                clearInterval( id );
                res.end();
            }
        }, 500 );
    } );
} );

app.listen( 3000, () => {
    console.log( "Example app listening on port 3000!" );
} );
