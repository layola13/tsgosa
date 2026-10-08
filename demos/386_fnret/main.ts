function mk(): (x: i32) => i32 {
  return (x: i32): i32 => { return x * 3; };
}
function main(): i32 {
  let f = mk();
  console.log(f(14));
  const g = mk();
  console.log(g(10));
  return 0;
}
