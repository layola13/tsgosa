const D = [[[1]], [[2]]];
function main(): i32 {
  const c = D.slice(1);
  console.log(c.length);
  console.log(c[0][0][0]);
  return 0;
}
