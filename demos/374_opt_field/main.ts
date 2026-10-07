interface P {
  a: i32;
  b?: i32;
}
function main(): i32 {
  const p: P = { a: 1 };
  console.log(p.a);
  console.log(p.b ?? 5);
  return 0;
}
