const N = [[1, 2], [3, 4]];
function v(): i32 {
  console.log(N[0]);
  return 0;
}
function s(): i32 {
  N[0][0] = 9;
  return N[0][0];
}
function a(): i32 {
  const r = N[0];
  return r[0];
}
function main(): i32 {
  return v() + s() + a();
}
