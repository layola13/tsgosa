interface P { x: i32; }
function main(): i32 {
  const o = { x: 5 } satisfies P;
  console.log(o.x);
  return 0;
}
