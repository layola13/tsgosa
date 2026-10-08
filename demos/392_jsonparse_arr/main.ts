interface P {
  xs: i32[];
  n: i32;
}
function main(): i32 {
  const p: P = JSON.parse("{\"xs\":[1,2,3],\"n\":4}");
  console.log(p.xs[0] + p.xs[2] + p.n);
  return 0;
}
