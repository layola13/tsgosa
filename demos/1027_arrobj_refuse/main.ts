interface P { x: i32; }
function main(): i32 {
  const a: P[] = [{ x: 1 }, { x: 2 }];
  console.log(a[1].x);
  console.log(a.length);
  return 0;
}
