const D = [[[1]], [[2, 3]]];
function main(): i32 {
  let s = 0;
  for (const m of D) {
    s = s + m[0][0] + m.length;
  }
  console.log(s);
  return 0;
}
