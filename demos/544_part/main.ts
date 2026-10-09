interface P { a: i32; }
function main(): i32 {
  const q: P = { a: 1 };
  console.log(q.a);
  return 0;
}
