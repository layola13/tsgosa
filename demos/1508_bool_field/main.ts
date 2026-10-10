interface B { x: i32; flag: boolean; }
function main(): i32 {
  const p: B = { x: 7, flag: true };
  console.log(p.x);
  console.log(p.flag ? 1 : 0);
  return 0;
}
