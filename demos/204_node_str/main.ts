import { escape, unescape } from "querystring";
import { encode, decode } from "punycode";

function main(): i32 {
  console.log(escape("a b"), unescape("a%20b"));
  console.log(encode("münchen"), decode("mnchen-3ya"));
  return 0;
}