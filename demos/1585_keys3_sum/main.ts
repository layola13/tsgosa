interface T { a: i32; b: i32; c: i32; }
function main(): i32 {
  const o: T = { a: 1, b: 2, c: 3 };
  console.log(Object.keys(o).length);
  console.log(o.a + o.b + o.c);
  return 0;
}
