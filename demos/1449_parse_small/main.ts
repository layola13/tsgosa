interface P { a: i32; }
function main(): i32 {
  const o: P = JSON.parse("{\"a\": 7}");
  console.log(o.a);
  return 0;
}
