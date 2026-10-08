interface P {
  a: i32;
  b: i32;
}
function main(): i32 {
  const p: P = JSON.parse("{\"a\":1,\"b\":2}");
  console.log(p.a + p.b);
  const q: P = JSON.parse("{\"a\":7}");
  console.log(q.a + q.b);
  return 0;
}
