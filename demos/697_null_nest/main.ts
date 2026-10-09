function gn(): string | null {
  return null;
}
function gq(): string | null {
  return "q";
}
console.log((gn() ?? gq()) ?? "d");
