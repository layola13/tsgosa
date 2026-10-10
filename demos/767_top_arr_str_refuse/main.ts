const M = ["a", 1];
const S = ["x", "yy"];
function m(): i32 {
  console.log(M.length);
  return 0;
}
function s(): i32 {
  console.log(S.slice(1).length);
  return 0;
}
function main(): i32 {
  return m() + s();
}
