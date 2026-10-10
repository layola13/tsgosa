const SCORES: i32[] = [10, 20, 30];
function total(): i32 {
  return SCORES[0] + SCORES[1] + SCORES[2] + SCORES.length;
}
function main(): i32 {
  console.log(total());
  console.log(SCORES[1]);
  console.log(SCORES.length);
  return 0;
}
