function getArr(): i32[] { return [1, 2, 3]; }
function main(): i32 {
  let t = 0;
  for (const v of getArr()) { t += v; }
  console.log(t);
  return 0;
}
